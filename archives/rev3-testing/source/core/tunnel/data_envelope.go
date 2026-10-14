package tunnel

import (
	"encoding/json"
	"errors"

	"itom.local/core/journal"
	"itom.local/core/mailbox"
	"itom.local/core/wire"
)

func canonicalDataSize(body []byte) (uint64, error) {
	n, err := mailbox.EncodedSize(len(body))
	if err == nil && n > dataBatchMIMEHardCap {
		err = mailbox.ErrLimit
	}
	return n, err
}

// One authenticated physical envelope is used in both directions. Quota
// authority remains asymmetric: only C2S registers client spends at the server.
// S2C is authorized by the authenticated server and its local durable journal.
func (l *lane) receiveDataEnvelope(m mailbox.Message, body []byte) error {
	charged, err := mailbox.DataProofCharge(m.MIME, body)
	if err != nil {
		l.traceEnvelopeResult("CHARGE_REJECTED", 0)
		return nil
	}
	report, payload, err := wire.OpenDataEnvelope(body, l.envelopeKey, charged)
	if err != nil {
		l.traceEnvelopeResult("AUTHENTICATION_REJECTED", 0)
		return nil
	}
	if report.Key.Account != l.account.ID || report.Key.User != l.user.ID || report.Attempt.Folder != m.Folder || !l.ownsReceiveFolder(m.Folder) {
		l.traceEnvelopeResult("SCOPE_REJECTED", 0)
		return nil
	}
	if l.side {
		// Reuse the authenticated-control registration contract. It enforces
		// the original grant authority, contiguous ordinals and exact budget.
		frame, err := wire.EncodeControl(0, report)
		if err != nil {
			return err
		}
		if err = l.process(frame); err != nil {
			return err
		}
		if _, ok := l.reports[report.Attempt.Digest]; !ok {
			_, err := l.data.GetGrant(grantRef(report.Key))
			if errors.Is(err, journal.ErrMissing) {
				return nil
			}
			if err != nil {
				return err
			}
			// A forward descriptor gap is exceptional. Retain bounded BODY
			// state while the existing durable prefix replay repairs that gap.
			return l.queuePendingRemote(m, body)
		}
		err = l.remoteDataPrepared(m, report, body, true)
		if err == nil {
			l.traceEnvelopeResult("REGISTERED_BODY", int(report.Total.Messages))
		}
		return err
	}
	if l.cipher == nil || report.Key.Session != l.cipher.Context().Session {
		return nil
	}
	_, records, err := wire.UnpackDataBatch(payload)
	if err != nil {
		return nil
	}
	for _, record := range records {
		if err = l.acceptRecord(m, record, false); err != nil {
			return err
		}
	}
	return nil
}

func (l *lane) traceEnvelopeResult(code string, ordinal int) {
	if l.diag == nil {
		return
	}
	raw, _ := json.Marshal(map[string]any{"ordinal": ordinal})
	l.diag.trace(traceEvent{Event: "data_envelope_receive", Code: code, Details: raw})
}
