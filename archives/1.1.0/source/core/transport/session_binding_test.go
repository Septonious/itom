package transport_test

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/netip"
	"path/filepath"
	"testing"

	"itom.local/core/journal"
	"itom.local/core/pool"
	"itom.local/core/secure"
	"itom.local/core/transport"
	"itom.local/core/wallet"
	"itom.local/core/wire"
)

func TestSessionRejectsRoutesFromOtherNoiseEpochsBeforeAnySpend(t *testing.T) {
	for _, id := range []wire.ID{{44}, {43}} {
		name := "different_session"
		if id == (wire.ID{43}) {
			name = "same_public_binding_independent_keys"
		}
		t.Run(name, func(t *testing.T) {
			f := serverPublications(t, transport.Fast, 4<<20)
			ce, se := sessionEpochs(t, id)
			ca, err := transport.NewCipher(ce)
			if err != nil {
				t.Fatal(err)
			}
			cb, err := transport.NewCipher(se)
			if err != nil {
				t.Fatal(err)
			}
			w, err := wallet.OpenBound(filepath.Join(t.TempDir(), "other-wallet.db"), f.store.StoreID(), ce)
			if err != nil {
				t.Fatal(err)
			}
			defer w.Close()
			scope := journal.Scope{Account: f.scope.Account, User: f.scope.User, Session: id}
			grant, err := f.store.IssueRemote(scope, pool.Amount{Bytes: 50000, Messages: 1})
			if err != nil {
				t.Fatal(err)
			}
			offer, err := f.store.RemoteOffer(grant.Ref)
			if err != nil {
				t.Fatal(err)
			}
			if err = w.Accept(offer); err != nil {
				t.Fatal(err)
			}
			held, err := f.store.Held(f.scope.Account, f.scope.User)
			if err != nil {
				t.Fatal(err)
			}
			client, server := f.client.Snapshot(), f.server.Snapshot()
			for name, route := range map[string]func() error{
				"client_publisher": func() error {
					_, err := transport.NewClientPublisher(f.client, transport.Fast, transport.DataLedger, ca, w, "account1", "fast", f.key)
					return err
				},
				"server_publisher": func() error {
					_, err := transport.NewServerPublisher(f.server, transport.Fast, transport.DataLedger, cb, f.store, scope, "fast", f.key)
					return err
				},
				"client_receiver": func() error {
					_, err := transport.NewClientReceiver(f.client, transport.DataLedger, ca, f.origin, f.key)
					return err
				},
				"server_receiver": func() error {
					_, err := transport.NewRemoteReceiver(f.server, transport.DataLedger, cb, f.store, "account1", f.scope.User, "fast", 9, f.key)
					return err
				},
			} {
				if err := route(); !errors.Is(err, transport.ErrProtocol) {
					t.Errorf("%s accepted another epoch: %v", name, err)
				}
			}
			if f.client.Snapshot() != client || f.server.Snapshot() != server {
				t.Fatal("rejected route mutated logical queues/credit")
			}
			entries, err := w.Entries()
			if err != nil || len(entries) != 1 || entries[0].Used != (wire.Quota{}) || len(entries[0].Pending) != 0 {
				t.Fatal("rejected route spent original grant", entries, err)
			}
			if after, err := f.store.Held(f.scope.Account, f.scope.User); err != nil || after != held {
				t.Fatal("route rejection changed retained quota", held, after, err)
			}
			// A wrapper around the SAME epoch is valid; public-label equality
			// alone was insufficient for the independent handshake above.
			same, err := transport.NewCipher(f.clientEpoch)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = transport.NewClientReceiver(f.client, transport.DataLedger, same, f.origin, f.key); err != nil {
				t.Fatal("exact original epoch poisoned by rejected routes", err)
			}
			if err = f.store.Audit(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestOppositeEndpointRoleCannotClaimAnUnboundLogicalSession(t *testing.T) {
	f := serverPublications(t, transport.Fast, 4<<20)
	w, err := wallet.OpenBound(filepath.Join(t.TempDir(), "wallet.db"), f.store.StoreID(), f.clientEpoch)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	for name, invalid := range map[string]func(*transport.Session) error{
		"client_publisher": func(s *transport.Session) error {
			_, err := transport.NewClientPublisher(s, transport.Fast, transport.DataLedger, f.serverKey, w, "account1", "fast", f.key)
			return err
		},
		"client_receiver": func(s *transport.Session) error {
			_, err := transport.NewClientReceiver(s, transport.DataLedger, f.serverKey, f.origin, f.key)
			return err
		},
		"server_publisher": func(s *transport.Session) error {
			_, err := transport.NewServerPublisher(s, transport.Fast, transport.DataLedger, f.clientKey, f.store, f.scope, "fast", f.key)
			return err
		},
		"server_receiver": func(s *transport.Session) error {
			_, err := transport.NewRemoteReceiver(s, transport.DataLedger, f.clientKey, f.store, "account1", f.scope.User, "fast", 9, f.key)
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			s := session(t, transport.DefaultLimits())
			if err := invalid(s); !errors.Is(err, transport.ErrProtocol) {
				t.Errorf("opposite endpoint role accepted: %v", err)
			}
			if _, err := transport.NewClientReceiver(s, transport.DataLedger, f.clientKey, f.origin, f.key); err != nil {
				t.Fatal("failed constructor claimed the fresh session", err)
			}
		})
	}
}

func TestExitRoleBindingIsAtomicAndCannotReplaceAConfiguredClient(t *testing.T) {
	ce, se := sessionEpochs(t, wire.ID{73})
	client, err := transport.NewCipher(ce)
	if err != nil {
		t.Fatal(err)
	}
	server, err := transport.NewCipher(se)
	if err != nil {
		t.Fatal(err)
	}
	policy := transport.ExitPolicyFunc(func(context.Context, string, transport.Target) (netip.AddrPort, error) {
		return netip.AddrPort{}, transport.OpenDenied
	})
	s := session(t, transport.DefaultLimits())
	if _, err = transport.NewExitServer(s, client, 256<<10, 1, policy, &net.Dialer{}); !errors.Is(err, transport.ErrProtocol) {
		t.Errorf("client epoch configured a server exit: %v", err)
	}
	if _, err = transport.NewClientDialer(s, 256<<10); err != nil {
		t.Fatal("failed exit constructor mutated OPEN role", err)
	}
	if _, err = transport.NewExitServer(s, server, 256<<10, 1, policy, &net.Dialer{}); !errors.Is(err, transport.ErrProtocol) {
		t.Fatal("configured client role replaced by server", err)
	}
	f := serverPublications(t, transport.Fast, 4<<20)
	other := session(t, transport.DefaultLimits())
	if _, err = transport.NewRemoteReceiver(other, transport.DataLedger, f.serverKey, f.store, f.scope.Account, f.scope.User, "fast", 9, f.key); err != nil {
		t.Fatal(err)
	}
	if _, err = transport.NewClientDialer(other, 256<<10); !errors.Is(err, transport.ErrProtocol) {
		t.Errorf("client OPEN role overrode a bound server receive epoch: %v", err)
	}
	if _, err = transport.NewExitServer(other, f.serverKey, 256<<10, 1, policy, &net.Dialer{}); err != nil {
		t.Fatal("failed dialer constructor poisoned valid server OPEN", err)
	}
}

func TestReplacementCipherRejectsLegacyNoiseEpochBeforeTransportAdmission(t *testing.T) {
	binding := secure.Binding{Server: wire.ID{1}, User: "12345678", Pair: wire.ID{2}, Session: wire.ID{74}, Handshake: wire.ID{4}, Epoch: 1, ConfigHash: [32]byte{5}}
	client, err := secure.NewHandshake(binding, bytes.Repeat([]byte{6}, 32), true)
	if err != nil {
		t.Fatal(err)
	}
	server, err := secure.NewHandshake(binding, bytes.Repeat([]byte{6}, 32), false)
	if err != nil {
		t.Fatal(err)
	}
	first, err := client.Write(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = server.Read(first); err != nil {
		t.Fatal(err)
	}
	second, err := server.Write(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = client.Read(second); err != nil {
		t.Fatal(err)
	}
	epoch, err := client.Epoch()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = transport.NewCipher(epoch); !errors.Is(err, transport.ErrProtocol) {
		t.Fatal("legacy epoch reached replacement transport admission", err)
	}
}
