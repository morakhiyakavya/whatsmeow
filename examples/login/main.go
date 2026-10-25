package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mdp/qrterminal/v3"
	"google.golang.org/protobuf/proto"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waCompanionReg"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
)

func main() {
	store.DeviceProps.Os = proto.String("venweb")
	store.DeviceProps.PlatformType = waCompanionReg.DeviceProps_DESKTOP.Enum()

	flag.Usage = func() {
		fmt.Fprintln(flag.CommandLine.Output(), "Usage: go run . [options]\nSet WHATSAPP_DATABASE_URL for PostgreSQL session storage.\nView incoming and outgoing private text messages. Press Ctrl+C to stop.")
		flag.PrintDefaults()
	}
	newSession := flag.Bool("new-session", false, "pair a new WhatsApp account instead of resuming a saved session")
	sessionID := flag.String("session", "", "resume the saved device JID printed after connection")
	flag.Parse()
	if flag.NArg() != 0 {
		log.Fatal("unexpected arguments; run without arguments, or use -h for help")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, *newSession, *sessionID); err != nil && ctx.Err() == nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, newSession bool, sessionID string) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	if newSession && sessionID != "" {
		return fmt.Errorf("use either -new-session or -session, not both")
	}
	container, err := openSessionStore(ctx, os.Getenv("WHATSAPP_DATABASE_URL"))
	if err != nil {
		return err
	}
	defer func() {
		if err := container.Close(); err != nil {
			log.Printf("close session database: %v", err)
		}
	}()

	device, err := selectSession(ctx, container, newSession, sessionID)
	if err != nil {
		return fmt.Errorf("load session: %w", err)
	}
	fmt.Println("Using PostgreSQL session storage (schema: whatsapp).")
	client := whatsmeow.NewClient(device, waLog.Stdout("WhatsApp", "WARN", true))
	client.InitialAutoReconnect = device.ID != nil
	client.DisableLoginAutoReconnect = true
	viewer := newMessageViewer(ctx, os.Stdout, device)
	clientEvents := make(chan any, 16)
	client.AddEventHandler(func(evt any) {
		switch v := evt.(type) {
		case *events.Message:
			if viewer.live(v) {
				info := v.Info
				go func() {
					historyCtx, stop := context.WithTimeout(ctx, 30*time.Second)
					defer stop()
					_, err := client.SendPeerMessage(historyCtx, client.BuildHistorySyncRequest(&info, historyBatchMessages))
					if err != nil && ctx.Err() == nil {
						viewer.historyFailed(info.Chat, err)
					}
				}()
			}
		case *events.HistorySync:
			viewer.history(client, v)
		case *events.Connected, *events.Disconnected, *events.ManualLoginReconnect, events.PermanentDisconnect:
			select {
			case clientEvents <- evt:
			case <-ctx.Done():
			}
		}
	})

	var qrChan <-chan whatsmeow.QRChannelItem
	if client.Store.ID == nil {
		qrChan, err = client.GetQRChannel(ctx)
		if err != nil {
			return fmt.Errorf("prepare QR login: %w", err)
		}
		fmt.Println("On your phone, open WhatsApp > Linked devices > Link a device.")
	} else {
		fmt.Println("Reconnecting with the saved session...")
	}
	if err := client.ConnectContext(ctx); err != nil {
		return fmt.Errorf("connect to WhatsApp: %w", err)
	}
	defer func() {
		cancel()
		client.Disconnect()
	}()
	return waitForLogin(ctx, client, qrChan, clientEvents)
}

func waitForLogin(ctx context.Context, client *whatsmeow.Client, qrChan <-chan whatsmeow.QRChannelItem, clientEvents <-chan any) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case evt := <-clientEvents:
			switch v := evt.(type) {
			case *events.Connected:
				fmt.Println("Connected to WhatsApp.")
				fmt.Printf("Session ID: %s\n", client.Store.GetJID())
				fmt.Println("Viewing private 1:1 text messages (IN/OUT) with names, numbers, and up to 10 older texts. Press Ctrl+C to disconnect.")
			case *events.Disconnected:
				fmt.Println("Connection interrupted. Retrying automatically; press Ctrl+C to stop.")
			case *events.ManualLoginReconnect:
				fmt.Println("Reconnecting to finish login...")
				client.Disconnect()
				client.InitialAutoReconnect = true
				if err := client.ConnectContext(ctx); err != nil {
					return fmt.Errorf("reconnect after pairing: %w", err)
				}
			case *events.LoggedOut:
				return fmt.Errorf("WhatsApp rejected the session (%s). Run the demo again to pair with a fresh QR code; remove any stale entry from your phone's Linked devices", v.Reason)
			case events.PermanentDisconnect:
				return fmt.Errorf("WhatsApp disconnected: %s", v.PermanentDisconnectDescription())
			}
		case item, ok := <-qrChan:
			if !ok {
				return fmt.Errorf("QR login ended before pairing; run the demo again")
			}
			switch item.Event {
			case whatsmeow.QRChannelEventCode:
				fmt.Println("Scan this QR code (it refreshes automatically):")
				qrterminal.GenerateHalfBlock(item.Code, qrterminal.L, os.Stdout)
			case whatsmeow.QRChannelSuccess.Event:
				fmt.Println("Pairing accepted. Session saved; waiting for the authenticated connection...")
				qrChan = nil
			case whatsmeow.QRChannelEventError:
				return fmt.Errorf("QR login failed: %w", item.Error)
			default:
				return fmt.Errorf("QR login stopped (%s); run the demo again", item.Event)
			}
		}
	}
}

func displayText(ctx context.Context, out io.Writer, device *store.Device, info types.MessageInfo, text string, edit bool) {
	direction := "IN"
	if info.IsFromMe {
		direction = "OUT"
	}
	if edit {
		direction += " EDIT"
	}
	sender, alt := info.Sender, info.SenderAlt
	if info.IsFromMe && device != nil {
		sender, alt = device.GetJID(), device.GetLID()
	}
	name, phone := resolvePerson(ctx, device, sender, alt, info.PushName)
	fmt.Fprintf(out, "[%s] %s chat=%q sender=%q number=%q id=%q %q\n", info.Timestamp.Local().Format("2006-01-02 15:04:05"), direction, chatLabel(ctx, device, info), name, phone, info.ID, text)
}
