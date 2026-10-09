package main

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/proto/waWeb"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

func viewerMessage(chat types.JID, n int) *events.Message {
	return &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{Chat: chat, Sender: chat, IsFromMe: n%2 == 0},
			ID:            fmt.Sprintf("id-%02d", n),
			Timestamp:     time.Unix(int64(n), 0),
		},
		Message: &waE2E.Message{Conversation: proto.String(fmt.Sprintf("message-%02d", n))},
	}
}

func viewerHistory(chat types.JID) *events.HistorySync {
	var messages []*waHistorySync.HistorySyncMsg
	// Deliberately supply reverse chronological order, as history may arrive that way.
	for n := 15; n >= 1; n-- {
		evt := viewerMessage(chat, n)
		messages = append(messages, &waHistorySync.HistorySyncMsg{Message: &waWeb.WebMessageInfo{
			Key:              &waCommon.MessageKey{ID: proto.String(evt.Info.ID), FromMe: proto.Bool(evt.Info.IsFromMe), RemoteJID: proto.String(chat.String())},
			MessageTimestamp: proto.Uint64(uint64(n)),
			Message:          evt.Message,
		}})
	}
	return &events.HistorySync{Data: &waHistorySync.HistorySync{
		SyncType:      waHistorySync.HistorySync_ON_DEMAND.Enum(),
		Conversations: []*waHistorySync.Conversation{{ID: proto.String(chat.String()), Messages: messages}},
	}}
}

func TestViewerContextFromHistory(t *testing.T) {
	for _, historyFirst := range []bool{true, false} {
		t.Run(fmt.Sprintf("history first=%t", historyFirst), func(t *testing.T) {
			var out bytes.Buffer
			viewer := newMessageViewer(context.Background(), &out, nil)
			chat := types.NewJID("123456789", types.DefaultUserServer)
			own := types.NewJID("987654321", types.DefaultUserServer)
			client := whatsmeow.NewClient(&store.Device{ID: &own}, nil)
			if historyFirst {
				viewer.history(client, viewerHistory(chat))
				if out.Len() != 0 {
					t.Fatal("history should populate buffers without dumping inactive chats")
				}
				if viewer.live(viewerMessage(chat, 16)) {
					t.Fatal("full context should not require another history request")
				}
			} else {
				if !viewer.live(viewerMessage(chat, 16)) {
					t.Fatal("missing context should request older history")
				}
				out.Reset()
				viewer.history(client, viewerHistory(chat))
			}
			output := out.String()
			if !strings.Contains(output, "10 older messages + latest") || !strings.Contains(output, "LATEST ") {
				t.Fatalf("context was not displayed: %q", output)
			}
			last := -1
			for n := 6; n <= 16; n++ {
				text := fmt.Sprintf(`"message-%02d"`, n)
				position := strings.Index(output, text)
				if position <= last || strings.Count(output, text) != 1 {
					t.Fatalf("missing, duplicated or out-of-order message %d: %q", n, output)
				}
				last = position
			}
			if strings.Contains(output, `"message-05"`) || len(viewer.chats[chat].messages) != contextMessages+1 {
				t.Fatal("chat buffer exceeded 10 older messages plus the latest")
			}
			if !strings.Contains(output, "] IN chat=") || !strings.Contains(output, "] OUT chat=") {
				t.Fatal("history should include both incoming and outgoing messages")
			}
			out.Reset()
			if viewer.live(viewerMessage(chat, 16)) || out.Len() != 0 {
				t.Fatal("duplicate live event should not repeat output or request history")
			}
			other := types.NewJID("111111111", types.DefaultUserServer)
			if !viewer.live(viewerMessage(other, 17)) || strings.Contains(out.String(), `"message-16"`) {
				t.Fatal("context from one chat leaked into another")
			}
		})
	}
}

func TestViewerUpdatesAndHistoryFailure(t *testing.T) {
	var out bytes.Buffer
	viewer := newMessageViewer(context.Background(), &out, nil)
	chat := types.NewJID("123456789", types.DefaultUserServer)
	if !viewer.live(viewerMessage(chat, 1)) {
		t.Fatal("first message should request context")
	}
	if viewer.live(viewerMessage(chat, 2)) {
		t.Fatal("an existing history request should not be duplicated")
	}
	viewer.historyFailed(chat, fmt.Errorf("phone offline"))
	if !viewer.live(viewerMessage(chat, 3)) {
		t.Fatal("a later event should retry history after a failed request")
	}
	out.Reset()
	edit := viewerMessage(chat, 3)
	edit.IsEdit = true
	edit.Message.Conversation = proto.String("Updated text")
	viewer.live(edit)
	if !strings.Contains(out.String(), "IN EDIT") || !strings.Contains(out.String(), "Updated text") {
		t.Fatal("edited message was not updated")
	}
	if len(viewer.chats[chat].messages) != 3 {
		t.Fatal("editing a message should replace it rather than growing the buffer")
	}
}

func TestViewerOnlyPrivateText(t *testing.T) {
	var out bytes.Buffer
	viewer := newMessageViewer(context.Background(), &out, nil)
	private := types.NewJID("123456789", types.DefaultUserServer)
	for _, server := range []string{types.GroupServer, types.BroadcastServer, types.NewsletterServer, types.MessengerServer} {
		chat := types.NewJID("123456789", server)
		if viewer.live(viewerMessage(chat, 1)) {
			t.Fatalf("non-private chat %s triggered a history request", server)
		}
		viewer.history(whatsmeow.NewClient(&store.Device{}, nil), viewerHistory(chat))
	}
	group := viewerMessage(private, 1)
	group.Info.IsGroup = true
	viewer.live(group)
	for _, msg := range []*waE2E.Message{
		{ImageMessage: &waE2E.ImageMessage{Caption: proto.String("Caption")}},
		{VideoMessage: &waE2E.VideoMessage{Caption: proto.String("Caption")}},
		{ReactionMessage: &waE2E.ReactionMessage{Text: proto.String("Reaction")}},
		nil,
	} {
		evt := viewerMessage(private, 2)
		evt.Message = msg
		if viewer.live(evt) {
			t.Fatal("non-text message triggered a history request")
		}
	}
	if out.Len() != 0 || len(viewer.chats) != 0 {
		t.Fatal("excluded messages reached the display or buffers")
	}
	client := whatsmeow.NewClient(&store.Device{ID: &private}, nil)
	history := viewerHistory(private)
	for _, msg := range history.Data.Conversations[0].Messages {
		if msg.Message.GetKey().GetFromMe() {
			msg.Message.Message = &waE2E.Message{ImageMessage: &waE2E.ImageMessage{Caption: proto.String("Hidden caption")}}
		}
	}
	viewer.history(client, history)
	viewer.live(viewerMessage(private, 16))
	if !strings.Contains(out.String(), "8 older messages + latest") || strings.Contains(out.String(), "Hidden caption") {
		t.Fatalf("history context did not filter media: %q", out.String())
	}
	if len(viewer.chats[private].messages) != 9 {
		t.Fatal("only text messages should consume buffer slots")
	}
}
