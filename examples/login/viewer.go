package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"slices"
	"strings"
	"sync"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

const contextMessages = 10

// Request a page so media interspersed in history doesn't consume text context.
const historyBatchMessages = 50

// Retain text and metadata, resolving current contact names when rendering.
type messageLine struct {
	info types.MessageInfo
	text string
	edit bool
}

type chatBuffer struct {
	messages         []messageLine
	latest           *messageLine
	historyRequested bool
	pending          bool
}

type messageViewer struct {
	mu     sync.Mutex
	out    io.Writer
	chats  map[types.JID]*chatBuffer
	ctx    context.Context
	device *store.Device
}

func newMessageViewer(ctx context.Context, out io.Writer, device *store.Device) *messageViewer {
	return &messageViewer{ctx: ctx, out: out, device: device, chats: make(map[types.JID]*chatBuffer)}
}

func makeMessageLine(evt *events.Message) messageLine {
	return messageLine{info: evt.Info, text: messageText(evt), edit: evt.IsEdit}
}

func (v *messageViewer) chat(chat types.JID) *chatBuffer {
	chat = chat.ToNonAD()
	if v.chats[chat] == nil {
		v.chats[chat] = &chatBuffer{}
	}
	return v.chats[chat]
}

func (h *chatBuffer) add(message messageLine, replace bool) bool {
	for i, old := range h.messages {
		if message.info.ID != "" && old.info.ID == message.info.ID {
			if !replace || old.text == message.text && old.edit == message.edit {
				return false
			}
			h.messages = slices.Delete(h.messages, i, i+1)
			break
		}
	}
	h.messages = append(h.messages, message)
	slices.SortStableFunc(h.messages, func(a, b messageLine) int { return a.info.Timestamp.Compare(b.info.Timestamp) })
	if len(h.messages) > contextMessages+1 {
		h.messages = slices.Delete(h.messages, 0, len(h.messages)-contextMessages-1)
	}
	return true
}

func (h *chatBuffer) older() []messageLine {
	var older []messageLine
	for _, message := range h.messages {
		if message.info.ID != h.latest.info.ID && !message.info.Timestamp.After(h.latest.info.Timestamp) {
			older = append(older, message)
		}
	}
	if len(older) > contextMessages {
		older = older[len(older)-contextMessages:]
	}
	return older
}

func (v *messageViewer) render(h *chatBuffer) {
	older := h.older()
	var block strings.Builder
	fmt.Fprintf(&block, "\n=== Private chat %q | %d older messages + latest ===\n", chatLabel(v.ctx, v.device, h.latest.info), len(older))
	for _, message := range older {
		displayText(v.ctx, &block, v.device, message.info, message.text, message.edit)
	}
	block.WriteString("LATEST ")
	displayText(v.ctx, &block, v.device, h.latest.info, h.latest.text, h.latest.edit)
	fmt.Fprint(v.out, block.String())
}

// live returns true when context is missing and should be requested from the phone.
func (v *messageViewer) live(evt *events.Message) bool {
	if messageText(evt) == "" {
		return false
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	chat := evt.Info.Chat.ToNonAD()
	h := v.chat(chat)
	message := makeMessageLine(evt)
	if !h.add(message, true) {
		return false
	}
	h.latest = &message
	v.render(h)
	if len(h.older()) < contextMessages && !h.historyRequested && evt.Info.ID != "" {
		h.historyRequested = true
		h.pending = true
		return true
	}
	return false
}

func (v *messageViewer) history(client *whatsmeow.Client, evt *events.HistorySync) {
	for _, conversation := range evt.Data.GetConversations() {
		chat, err := types.ParseJID(conversation.GetID())
		if err != nil {
			log.Printf("skip history with invalid chat: %v", err)
			continue
		}
		chat = chat.ToNonAD()
		if !privateChat(chat) {
			continue
		}
		v.mu.Lock()
		h := v.chats[chat]
		for _, historical := range conversation.GetMessages() {
			if historical.GetMessage().GetMessage() == nil {
				continue
			}
			message, err := client.ParseWebMessage(chat, historical.GetMessage())
			if err != nil {
				log.Printf("skip historical message: %v", err)
				continue
			}
			if messageText(message) == "" {
				continue
			}
			if h == nil {
				h = v.chat(chat)
			}
			h.add(makeMessageLine(message), false)
		}
		if h != nil && h.pending && h.latest != nil {
			v.render(h)
			h.pending = false
		}
		v.mu.Unlock()
	}
}

func (v *messageViewer) historyFailed(chat types.JID, err error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	h := v.chat(chat)
	h.historyRequested = false
	h.pending = false
	fmt.Fprintf(v.out, "Older history unavailable for chat %q: %q. Showing buffered messages.\n", chat.String(), err.Error())
}
