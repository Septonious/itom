package mailstream

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Pressure can arrive after a live control timeout. Exercise the same native
// transport with every account nearly full, so ordinary bulk cannot hide the
// lost historical-recovery retry behind another healthy mailbox.
func TestHTTPSRecoversAfterLiveControlFailureAndNewStoragePressure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	accounts, _ := applicationMailServerAccounts(t, true, false, nil, nil, 10)
	server, err := NewStoredIMAPLink(ctx, "pressure-transition", accounts, false, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	client, err := NewStoredIMAPLink(ctx, "pressure-transition", accounts, true, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	key := bytes.Repeat([]byte{7}, 32)
	go Serve(ctx, server, "pressure-transition", key, (&net.Dialer{}).DialContext, nil)
	session, err := Connect(ctx, client, "pressure-transition", key)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	waitRetention(t, ctx, func() bool {
		for _, a := range server.accounts {
			a.mu.Lock()
			ready := a.writer != nil && a.control != nil && a.control.cursor.Load() > 0 && a.maintenance.remoteStartOut.Validity != 0
			a.mu.Unlock()
			if !ready {
				return false
			}
		}
		return true
	}, "account control watermarks never attached")
	// Generated old DATA is owned by an earlier session. Conservative crash
	// debt can exceed these physical bytes, but only an audited sweep may refund it.
	for _, config := range accounts {
		p, e := openMailPipe(ctx, config)
		if e != nil {
			t.Fatal(e)
		}
		seedCollectionFolder(t, p, server.segmentFolder(SessionID{0x55}, false, 0), 8)
		p.close()
	}
	for _, a := range server.accounts {
		a.mu.Lock()
		a.maintenance.inventoried = false
		a.maintenance.recoveryReady = false
		a.maintenance.boundaryOut = uidWatermark{}
		a.maintenance.clearedIn = uidWatermark{a.control.validity, 0}
		a.maintenance.clearedOut = a.maintenance.initialOut
		a.mu.Unlock()
		server.reclaimer.folder(server.controlFolder(false))
		server.reclaimer.stage("expunge")
		server.reclaimer.pause("MAINTENANCE_TIMEOUT", a)
	}
	for _, a := range client.accounts {
		a.mu.Lock()
		debt := prototypePhysicalBudget - reservationQuantum - a.spent
		if debt <= 0 {
			a.mu.Unlock()
			t.Fatal("fixture account has no room for historical debt")
		}
		a.spent += debt
		a.recovered += debt
		a.reserved = a.spent
		err = client.store.put(a.storageKey, a.reserved)
		a.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
	}
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.Copy(w, r.Body) }))
	defer origin.Close()
	transport := origin.Client().Transport.(*http.Transport).Clone()
	transport.ForceAttemptHTTP2 = false
	transport.DialContext = func(ctx context.Context, _, target string) (net.Conn, error) { return session.OpenTCP(ctx, target) }
	defer transport.CloseIdleConnections()
	payload := bytes.Repeat([]byte("recovery must unblock both repeated uploads\n"), 200000)
	for run := 0; run < 2; run++ {
		response, e := (&http.Client{Transport: transport, Timeout: 20 * time.Second}).Post(origin.URL, "application/octet-stream", bytes.NewReader(payload))
		if e != nil {
			t.Fatalf("HTTPS upload%d blocked while recovered retention stayed charged: %v", run, e)
		}
		got, e := io.ReadAll(response.Body)
		response.Body.Close()
		if e != nil || !bytes.Equal(got, payload) {
			t.Fatal("recovered HTTPS integrity", run, e, len(got))
		}
	}
	waitRetention(t, ctx, func() bool { return client.Snapshot().RecoveredBytes == 0 }, "historical debt not refunded after verified recovery")
	if server.Snapshot().Reclamation.Paused {
		t.Fatal("control quarantine remained after audited recovery")
	}
	if client.Snapshot().Ready != 10 || server.Snapshot().Ready != 10 {
		t.Fatal("recovery lost account readiness")
	}
}
