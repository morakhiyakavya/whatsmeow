package main

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waAdv"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
	_ "modernc.org/sqlite"
)

func TestSessionPersistence(t *testing.T) {
	ctx := context.Background()
	dsn := "file:" + filepath.ToSlash(filepath.Join(t.TempDir(), "session.db")) + "?_pragma=foreign_keys(1)"
	container, err := sqlstore.New(ctx, "sqlite", dsn, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = container.Close() })
	device, err := container.GetFirstDevice(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if device.ID != nil {
		t.Fatal("new database should not have a paired device")
	}
	jid := types.NewJID("123456789", types.DefaultUserServer)
	device.ID = &jid
	device.Account = &waAdv.ADVSignedDeviceIdentity{
		Details:             []byte{},
		AccountSignature:    make([]byte, 64),
		AccountSignatureKey: make([]byte, 32),
		DeviceSignature:     make([]byte, 64),
	}
	if err := device.Save(ctx); err != nil {
		t.Fatal(err)
	}
	if err := container.Close(); err != nil {
		t.Fatal(err)
	}

	container, err = sqlstore.New(ctx, "sqlite", dsn, nil)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := container.GetFirstDevice(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ID == nil || *loaded.ID != jid {
		t.Fatalf("saved session did not survive reopening: got %v", loaded.ID)
	}
}

func TestPermanentDisconnectStopsWaiting(t *testing.T) {
	for _, tc := range []struct {
		name  string
		event events.PermanentDisconnect
		want  string
	}{
		{"rejected session", &events.LoggedOut{OnConnect: true, Reason: events.ConnectFailureLoggedOut}, "fresh QR code"},
		{"duplicate connection", &events.StreamReplaced{}, "stream replaced"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			clientEvents := make(chan any, 1)
			// Exercise both saved-session and active QR login waits.
			for _, qrChan := range []<-chan whatsmeow.QRChannelItem{nil, make(chan whatsmeow.QRChannelItem)} {
				clientEvents <- tc.event
				err := waitForLogin(ctx, nil, qrChan, clientEvents)
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("expected actionable disconnect error containing %q, got %v", tc.want, err)
				}
			}
		})
	}
}

func TestPairingReconnectRetriesNetworkFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	jid := types.NewJID("123456789", types.DefaultUserServer)
	client := whatsmeow.NewClient(&store.Device{ID: &jid}, nil)
	defer client.Disconnect()
	defer cancel()

	attempts := make(chan struct{}, 4)
	// Simulate the TLS timeout before opening a socket; no external network is used.
	client.SetWebsocketHTTPClient(&http.Client{Transport: &http.Transport{
		DialContext: func(context.Context, string, string) (net.Conn, error) {
			attempts <- struct{}{}
			return nil, context.DeadlineExceeded
		},
	}})
	clientEvents := make(chan any, 4)
	clientEvents <- &events.ManualLoginReconnect{}
	result := make(chan error, 1)
	go func() { result <- waitForLogin(ctx, client, nil, clientEvents) }()

	for range 2 {
		select {
		case <-attempts:
		case <-ctx.Done():
			t.Fatal("post-pairing connection failure was not retried")
		case err := <-result:
			t.Fatalf("login wait stopped before retrying: %v", err)
		}
	}
	cancel()
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("cancellation should stop cleanly: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("login wait did not stop after cancellation")
	}
}

func TestMessageDisplay(t *testing.T) {
	for _, tc := range []struct {
		name string
		msg  *waE2E.Message
		own  bool
		want string
	}{
		{"plain text", &waE2E.Message{Conversation: proto.String("Hello")}, false, `"Hello"`},
		{"extended text", &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto.String("Reply")}}, false, `"Reply"`},
		{"terminal controls", &waE2E.Message{Conversation: proto.String("Hi\x1b[2J")}, false, `"Hi\x1b[2J"`},
		{"outgoing", &waE2E.Message{Conversation: proto.String("Hello")}, true, `"Hello"`},
		{"linked-device outgoing", &waE2E.Message{DeviceSentMessage: &waE2E.DeviceSentMessage{Message: &waE2E.Message{Conversation: proto.String("From my phone")}}}, true, `"From my phone"`},
		{"image caption", &waE2E.Message{ImageMessage: &waE2E.ImageMessage{Caption: proto.String("Photo")}}, false, ""},
		{"outgoing voice note", &waE2E.Message{AudioMessage: &waE2E.AudioMessage{}}, true, ""},
		{"reaction", &waE2E.Message{ReactionMessage: &waE2E.ReactionMessage{Text: proto.String("Hi")}}, false, ""},
		{"non-text", &waE2E.Message{}, false, ""},
		{"unavailable", nil, true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			viewer := newMessageViewer(context.Background(), &out, nil)
			viewer.live((&events.Message{
				Info: types.MessageInfo{MessageSource: types.MessageSource{
					Chat:     types.NewJID("123456789", types.DefaultUserServer),
					Sender:   types.NewJID("123456789", types.DefaultUserServer),
					IsFromMe: tc.own,
				}},
				RawMessage: tc.msg,
			}).UnwrapRaw())
			if tc.want == "" && out.Len() != 0 || tc.want != "" && !strings.Contains(out.String(), tc.want) {
				t.Fatalf("unexpected display: %q; wanted text %q", out.String(), tc.want)
			}
			if tc.want == "" {
				return
			}
			if strings.ContainsRune(out.String(), '\x1b') {
				t.Fatal("unescaped terminal control character in output")
			}
			direction := "IN"
			if tc.own {
				direction = "OUT"
			}
			if !strings.Contains(out.String(), "] "+direction+" chat=") {
				t.Fatalf("wrong direction in %q", out.String())
			}
		})
	}
}
