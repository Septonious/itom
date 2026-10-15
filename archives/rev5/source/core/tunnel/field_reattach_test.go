package tunnel

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"itom.local/core/mailbox"
	"itom.local/core/servercfg"
	"itom.local/core/wire"
	"testing"
)

func TestFieldDelayedProbeAndForeignChallengeCannotReattachLiveCarrier(t *testing.T) {
	key := make([]byte, 32)
	u := servercfg.User{ID: "12345678", Key: hex.EncodeToString(key), Up: "itom-up", Down: "itom-down"}
	m := &memoryMail{rows: map[string]map[uint32][]byte{}, next: map[string]uint32{}}
	if err := m.Ensure(context.Background(), u.Down); err != nil {
		t.Fatal(err)
	}
	l := &lane{ctx: context.Background(), side: true, user: u, account: servercfg.Account{ID: "a01"}, cipher: cleanupFixtureEpoch(t), mail: m,
		sendFolder: u.Down, preauthClient: wire.ID{3}, preauthServer: wire.ID{2}, helloUID: 10, helloValidity: 1}
	makeMail := func(magic string, v any) mailbox.Message {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		mime, err := mailbox.Encode(append([]byte(magic), raw...))
		if err != nil {
			t.Fatal(err)
		}
		return mailbox.Message{Validity: 1, UID: 11, MIME: mime}
	}
	probe := preauthProbe{Version: transportWireVersion, Capabilities: transportCapabilities, ClientNonce: l.preauthClient}
	probe.MAC = preauthMAC(key, "probe", l.cipher.Context().Server, u, l.account.ID, probe.ClientNonce, wire.ID{}, transportCapabilities)
	if err := l.read([]mailbox.Message{makeMail(transportProbeMagic, probe)}); err != nil {
		t.Fatal(err)
	}
	if m.next[u.Down] != 0 {
		t.Fatal("late probe from authenticated client published a false reattach challenge")
	}
	probe.ClientNonce = wire.ID{4}
	probe.MAC = preauthMAC(key, "probe", l.cipher.Context().Server, u, l.account.ID, probe.ClientNonce, wire.ID{}, transportCapabilities)
	if err := l.read([]mailbox.Message{makeMail(transportProbeMagic, probe)}); err != nil {
		t.Fatal(err)
	}
	if m.next[u.Down] != 1 {
		t.Fatal("genuine new client could not obtain a challenge")
	}
	l.side = false
	challenge := preauthChallenge{Version: transportWireVersion, Capabilities: transportCapabilities, ClientNonce: wire.ID{4}, ServerNonce: wire.ID{5}}
	challenge.MAC = preauthMAC(key, "challenge", l.cipher.Context().Server, u, l.account.ID, challenge.ClientNonce, challenge.ServerNonce, transportCapabilities)
	if err := l.read([]mailbox.Message{makeMail(transportChallengeMagic, challenge)}); err != nil {
		t.Fatal("another client's probe response disconnected live carrier", err)
	}
	challenge.ClientNonce = wire.ID{}
	challenge.MAC = preauthMAC(key, "challenge", l.cipher.Context().Server, u, l.account.ID, challenge.ClientNonce, challenge.ServerNonce, transportCapabilities)
	if err := l.read([]mailbox.Message{makeMail(transportChallengeMagic, challenge)}); err == nil || err.Error() != "CARRIER_REATTACH" {
		t.Fatal("real server restart must still reconnect", err)
	}
}
