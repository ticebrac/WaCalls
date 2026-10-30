package main

import (
	"context"
	"strings"

	"go.mau.fi/whatsmeow/types"
)

// WhatsApp addresses the same person either by phone number (@s.whatsapp.net)
// or by a hidden LID (@lid), and picks one per message. Without normalization
// the two forms become two separate chat rows in the store, so a reply lands in
// a different chat than the message that triggered it.
//
// Everything persisted under chat_jid is therefore canonicalized to the
// phone-number form whenever the mapping is known, keeping one chat per person.

// canonicalChatJID resolves the chat of an incoming event to its phone-based JID.
func (s *Session) canonicalChatJID(ctx context.Context, src types.MessageSource) types.JID {
	chat := src.Chat.ToNonAD()
	if chat.Server != types.HiddenUserServer {
		return chat
	}
	if pn := s.userPNForLID(ctx, chat); !pn.IsEmpty() {
		return pn
	}
	// Fall back to the alternative address the event itself carries.
	if alt := s.pnFromAlts(src); !alt.IsEmpty() {
		return alt
	}
	return chat
}

// canonicalPeer resolves an address supplied by the operator (or a group member
// field) from LID to phone number form, leaving it untouched otherwise.
func (s *Session) canonicalPeer(ctx context.Context, jid types.JID) types.JID {
	jid = jid.ToNonAD()
	if jid.Server != types.HiddenUserServer {
		return jid
	}
	if pn := s.userPNForLID(ctx, jid); !pn.IsEmpty() {
		return pn
	}
	return jid
}

// canonicalJIDFromInput parses operator input and resolves LID to phone form.
func (s *Session) canonicalJIDFromInput(ctx context.Context, input string) types.JID {
	return s.canonicalPeer(ctx, chatJIDFromInput(input))
}

// peerFromInput resolves what the operator supplies to start a call: either a
// bare number (country code applied) or a full JID, in which case a LID is
// resolved to the real phone number so the call is not placed to a bogus user.
func (s *Session) peerFromInput(ctx context.Context, input string) types.JID {
	input = strings.TrimSpace(input)
	if strings.Contains(input, "@") {
		return s.canonicalPeer(ctx, chatJIDFromInput(input))
	}
	return types.NewJID(withDefaultCountry(normalizePhone(input)), types.DefaultUserServer)
}

func (s *Session) userPNForLID(ctx context.Context, lid types.JID) types.JID {
	if lid.Server != types.HiddenUserServer {
		return types.JID{}
	}
	pn, err := s.client.Store.LIDs.GetPNForLID(ctx, lid)
	if err != nil || pn.User == "" {
		return types.JID{}
	}
	return pn.ToNonAD()
}

// pnFromAlts picks the phone-based JID the event carries as the alternative
// address of the peer, preferring the side opposite to IsFromMe and skipping
// our own address so a self-echo is never mistaken for the peer.
func (s *Session) pnFromAlts(src types.MessageSource) types.JID {
	alts := []types.JID{src.SenderAlt, src.RecipientAlt}
	if src.IsFromMe {
		alts[0], alts[1] = src.RecipientAlt, src.SenderAlt
	}
	for _, alt := range alts {
		alt = alt.ToNonAD()
		if alt.Server != types.DefaultUserServer || alt.User == "" {
			continue
		}
		if s.isOwnUser(alt) {
			continue
		}
		return alt
	}
	return types.JID{}
}

func (s *Session) isOwnUser(jid types.JID) bool {
	if id := s.client.Store.ID; id != nil && id.User == jid.User {
		return true
	}
	if lid := s.client.Store.LID; !lid.IsEmpty() && lid.User == jid.User {
		return true
	}
	return false
}
