package signaling

import (
	"bytes"
	"context"
	"testing"

	"wacalls/internal/voip/core"
	"wacalls/internal/voip/wanode"

	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/types"
)

type fakeSocket struct {
	devices   []types.JID
	preKeyDev []types.JID
	token     []byte
}

var _ core.VoipSocket = (*fakeSocket)(nil)

func (f *fakeSocket) OwnPN() types.JID {
	return types.NewJID("5511999998888", types.DefaultUserServer)
}

func (f *fakeSocket) OwnLID() types.JID { return types.JID{} }

func (f *fakeSocket) AccountDeviceIdentityNode() (waBinary.Node, bool) {
	return waBinary.Node{Tag: "device-identity", Content: []byte{0x01}}, true
}

func (f *fakeSocket) SendNode(context.Context, waBinary.Node) error { return nil }

func (f *fakeSocket) Query(context.Context, waBinary.Node) (*waBinary.Node, error) {
	return nil, nil
}

func (f *fakeSocket) GetUSyncDevices(context.Context, []types.JID) ([]types.JID, error) {
	return f.devices, nil
}

func (f *fakeSocket) AssertSessions(context.Context, []types.JID, bool) error { return nil }

func (f *fakeSocket) CreateParticipantNodes(_ context.Context, devices []types.JID, _ []byte, _ waBinary.Attrs) ([]waBinary.Node, bool, error) {
	nodes := make([]waBinary.Node, 0, len(devices))
	includeIdentity := false
	for _, d := range devices {
		encType := "msg"
		for _, pk := range f.preKeyDev {
			if pk == d {
				encType = "pkmsg"
				includeIdentity = true
			}
		}
		nodes = append(nodes, waBinary.Node{
			Tag:   "to",
			Attrs: waBinary.Attrs{"jid": d},
			Content: []waBinary.Node{{
				Tag:     "enc",
				Attrs:   waBinary.Attrs{"v": "2", "type": encType, "count": "0"},
				Content: []byte{0xab, 0xcd},
			}},
		})
	}
	return nodes, includeIdentity, nil
}

func (f *fakeSocket) DecryptCallKey(context.Context, types.JID, *waBinary.Node) ([]byte, error) {
	return nil, nil
}

func (f *fakeSocket) GetTCToken(context.Context, types.JID) ([]byte, error) { return f.token, nil }

func (f *fakeSocket) ResolveLIDForPN(_ context.Context, pn types.JID) types.JID { return pn }

// buildOffer returns the <offer> node of a generated <call> stanza and fails
// the test when the wrapper does not hold exactly one action.
func buildOffer(t *testing.T, sock core.VoipSocket, peer string) *waBinary.Node {
	t.Helper()
	stanza, err := BuildOfferStanza(
		context.Background(), sock, "E36D544B492021D1111FDE82193A4674",
		[]byte{0x01, 0x02, 0x03}, types.NewJID(peer, types.DefaultUserServer), false,
	)
	if err != nil {
		t.Fatalf("BuildOfferStanza: %v", err)
	}
	actions := wanode.NodeChildren(&stanza)
	if len(actions) != 1 {
		t.Fatalf("expected 1 action in <call>, got %d", len(actions))
	}
	if actions[0].Tag != "offer" {
		t.Fatalf("expected <offer>, got <%s>", actions[0].Tag)
	}
	offer := actions[0]
	return &offer
}

func childTagList(t *testing.T, n *waBinary.Node) []string {
	t.Helper()
	var tags []string
	for _, c := range wanode.NodeChildren(n) {
		tags = append(tags, c.Tag)
	}
	return tags
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// The documented single-device position is honoured structurally: the wrapped
// call key sits between <capability> and <encopt> (WhatsApp's servers answer
// error 439 when something else takes that slot).
func TestBuildOfferSingleDeviceChildOrder(t *testing.T) {
	sock := &fakeSocket{devices: []types.JID{types.NewJID("5511888887777", types.DefaultUserServer)}}
	offer := buildOffer(t, sock, "5511888887777")

	want := []string{"audio", "audio", "net", "capability", "destination", "encopt"}
	if got := childTagList(t, offer); !equalStrings(got, want) {
		t.Fatalf("offer children = %v, want %v", got, want)
	}
	if targets := offerDestination(t, offer); len(targets) != 1 {
		t.Fatalf("offer targets %d devices, want 1", len(targets))
	}
}

// offerDestination returns the <to> entries the offer targets.
func offerDestination(t *testing.T, offer *waBinary.Node) []waBinary.Node {
	t.Helper()
	for _, c := range wanode.NodeChildren(offer) {
		if c.Tag == "destination" {
			return wanode.NodeChildren(&c)
		}
	}
	t.Fatal("offer carries no <destination>")
	return nil
}

// A callee device we have no established Signal session with gets its call key
// wrapped with pre-keys (type="pkmsg"). WhatsApp answers those with
// terminate reason="enc", so a mixed offer must not carry them.
func TestBuildOfferDropsPreKeyDevices(t *testing.T) {
	phone := types.NewJID("5511888887777", types.DefaultUserServer)
	companion := types.NewJID("5511888887777", "lid")
	sock := &fakeSocket{
		devices:   []types.JID{phone, companion},
		preKeyDev: []types.JID{companion},
	}
	offer := buildOffer(t, sock, "5511888887777")

	want := []string{"audio", "audio", "net", "capability", "destination", "encopt"}
	if got := childTagList(t, offer); !equalStrings(got, want) {
		t.Fatalf("offer children = %v, want %v (pre-key device must be dropped)", got, want)
	}
	targets := offerDestination(t, offer)
	if len(targets) != 1 {
		t.Fatalf("offer targets %d devices, want only the established-session one", len(targets))
	}
	if hasEncType(targets, "pkmsg") {
		t.Fatal("offer still contains a pkmsg <enc>")
	}
}

// With no established session anywhere the offer still has to be attempted:
// the pre-key wrapping may be rejected, but refusing to dial is worse.
func TestBuildOfferFallsBackToPreKeys(t *testing.T) {
	phone := types.NewJID("5511888887777", types.DefaultUserServer)
	sock := &fakeSocket{devices: []types.JID{phone}, preKeyDev: []types.JID{phone}}
	offer := buildOffer(t, sock, "5511888887777")

	want := []string{"audio", "audio", "net", "capability", "destination", "encopt", "device-identity"}
	if got := childTagList(t, offer); !equalStrings(got, want) {
		t.Fatalf("offer children = %v, want %v", got, want)
	}
	if !hasEncType(offerDestination(t, offer), "pkmsg") {
		t.Fatal("fallback offer must still carry the pre-key <enc>")
	}
}

func TestBuildOfferMultiDeviceWrapsDestination(t *testing.T) {
	sock := &fakeSocket{devices: []types.JID{
		types.NewJID("5511888887777", types.DefaultUserServer),
		types.NewJID("5511888887777", "lid"),
	}}
	offer := buildOffer(t, sock, "5511888887777")

	want := []string{"audio", "audio", "net", "capability", "destination", "encopt"}
	if got := childTagList(t, offer); !equalStrings(got, want) {
		t.Fatalf("offer children = %v, want %v", got, want)
	}
	for _, c := range wanode.NodeChildren(offer) {
		if c.Tag != "destination" {
			continue
		}
		if n := len(wanode.NodeChildren(&c)); n != 2 {
			t.Fatalf("destination holds %d <to>, want 2", n)
		}
	}
}

func TestBuildOfferPrivacyComesFirst(t *testing.T) {
	sock := &fakeSocket{
		devices: []types.JID{types.NewJID("5511888887777", types.DefaultUserServer)},
		token:   []byte{0x99},
	}
	offer := buildOffer(t, sock, "5511888887777")

	got := childTagList(t, offer)
	if len(got) == 0 || got[0] != "privacy" {
		t.Fatalf("first offer child = %v, want privacy first", got)
	}
}

// Without any encryptable callee device the offer would carry an empty
// <destination>, which the server rejects outright.
func TestBuildOfferNoDevicesFails(t *testing.T) {
	sock := &fakeSocket{}
	if _, err := BuildOfferStanza(
		context.Background(), sock, "E36D544B492021D1111FDE82193A4674",
		[]byte{0x01}, types.NewJID("5511888887777", types.DefaultUserServer), false,
	); err == nil {
		t.Fatal("expected an error when no devices can be encrypted")
	}
}

// The offer/accept capability blob ends in 0x13; the preaccept one ends in
// 0x07. The build previously shipped the preaccept bytes as the offer blob.
func TestCapabilityBlobsAreDistinct(t *testing.T) {
	if bytes.Equal(capabilityOffer, capabilityPreaccept) {
		t.Fatal("offer and preaccept capability blobs must differ")
	}
	if capabilityOffer[len(capabilityOffer)-1] != 0x13 {
		t.Fatalf("offer capability ends %#x, want 0x13", capabilityOffer[len(capabilityOffer)-1])
	}
	if capabilityPreaccept[len(capabilityPreaccept)-1] != 0x07 {
		t.Fatalf("preaccept capability ends %#x, want 0x07", capabilityPreaccept[len(capabilityPreaccept)-1])
	}
}
