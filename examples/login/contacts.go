package main

import (
	"context"
	"fmt"

	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

func privateChat(chat types.JID) bool {
	switch chat.Server {
	case types.DefaultUserServer, types.HiddenUserServer, types.HostedServer, types.HostedLIDServer:
		return true
	default:
		return false
	}
}

func messageText(evt *events.Message) string {
	if evt == nil || evt.Info.IsGroup || !privateChat(evt.Info.Chat) {
		return ""
	}
	if text := evt.Message.GetConversation(); text != "" {
		return text
	}
	return evt.Message.GetExtendedTextMessage().GetText()
}

func normalUser(jid types.JID) types.JID {
	jid = jid.ToNonAD()
	if jid.Server == types.HostedServer {
		jid.Server = types.DefaultUserServer
	} else if jid.Server == types.HostedLIDServer {
		jid.Server = types.HiddenUserServer
	}
	return jid
}

func firstName(names ...string) string {
	for _, name := range names {
		if name != "" && name != "-" {
			return name
		}
	}
	return ""
}

func resolvePerson(ctx context.Context, device *store.Device, jid, alt types.JID, fallback string) (string, string) {
	jid, alt = normalUser(jid), normalUser(alt)
	pn := types.EmptyJID
	if jid.Server == types.DefaultUserServer {
		pn = jid
	} else if alt.Server == types.DefaultUserServer {
		pn = alt
	} else if jid.Server == types.HiddenUserServer && device != nil && device.LIDs != nil {
		pn, _ = device.LIDs.GetPNForLID(ctx, jid)
	}
	phone := "unavailable"
	if pn.Server == types.DefaultUserServer && pn.User != "" {
		phone = "+" + pn.User
	}
	if device != nil && !jid.IsEmpty() && (jid == normalUser(device.GetJID()) || jid == normalUser(device.GetLID())) {
		own := normalUser(device.GetJID())
		if own.Server == types.DefaultUserServer && own.User != "" {
			phone = "+" + own.User
		}
		return firstName(device.PushName, fallback, "You"), phone
	}
	var full, first, push, business string
	if device != nil && device.Contacts != nil {
		for _, candidate := range []types.JID{pn, jid, alt} {
			if candidate.IsEmpty() {
				continue
			}
			contact, err := device.Contacts.GetContact(ctx, candidate)
			if err != nil {
				continue
			}
			full = firstName(full, contact.FullName)
			first = firstName(first, contact.FirstName)
			push = firstName(push, contact.PushName)
			business = firstName(business, contact.BusinessName)
			if phone == "unavailable" && contact.RedactedPhone != "" {
				phone = contact.RedactedPhone + " (partial)"
			}
		}
	}
	return firstName(full, first, push, business, fallback, "Unknown contact"), phone
}

func chatLabel(ctx context.Context, device *store.Device, info types.MessageInfo) string {
	alt, fallback := info.SenderAlt, info.PushName
	if info.IsFromMe {
		alt, fallback = info.RecipientAlt, ""
	}
	name, phone := resolvePerson(ctx, device, info.Chat, alt, fallback)
	if phone == "unavailable" {
		return fmt.Sprintf("%s (number unavailable; ID %s)", name, info.Chat.ToNonAD())
	}
	return fmt.Sprintf("%s (%s)", name, phone)
}
