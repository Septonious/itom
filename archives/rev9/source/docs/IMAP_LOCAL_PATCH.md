# Local go-imap patch

Base: github.com/emersion/go-imap/v2 v2.0.0-beta.8, original MIT license retained in core/third_party/go-imap/LICENSE. Core and native modules explicitly replace this dependency with the same local source, so Android and CLI cannot silently use different behavior.

Local changes (full diff: GO_IMAP_PATCH.diff):

1. imapclient commandEncoder.Literal honours advertised LITERAL+ for APPEND body literals of all permitted sizes. LITERAL- still uses synchronizing literals above4096 bytes; absent capabilities remain synchronizing. Wire quota, APPEND count, command completion and durable accounting unchanged.
2. imapserver test capability advertisement does not advertise LITERAL- alongside LITERAL+, as RFC7888 forbids. This affects the bundled test server; ITOM production is an IMAP client, not this server.

RFC: https://www.rfc-editor.org/rfc/rfc7888.html. Mobile calibration must prove small APPEND uses1 RTT in both modes, large uses2 RTT for LITERAL- and1 RTT for LITERAL+. Refusal, cancellation, Unknown outcome and proof gates must still pass; there is no retry of APPEND.

A provider that does not advertise LITERAL+ retains the old wire behavior. Performance improvements must be reported by capability rather than assumed for all providers. Field4 baseline retains its original client implementation; the harness server-only advertisement fix is reported as test instrumentation.

UID FETCH: update-only UID/FLAGS/MODSEQ не потребляет слот полного ответа; SIZE может идти после BODY. Полные UID/size/body/scope проверки остаются в mailbox, bounded retry применяется только к неразрешённым UID. Raw-peer regression проверяет оба порядка SIZE/BODY, несколько updates и ровно один BODY на UID.


FIELD42 / field21: BINARY response parsing reads the optional partial origin
before the literal, using the same bounded origin parser as BODY. Binary
section matching checks the requested part and origin (a complete section with
no origin is only compatible with a zero-origin request). Requested BINARY is
also included in UID completion tracking so HEADER/SIZE-only updates cannot
consume the decoded body slot. RFC 3516 section 4.3:
https://www.rfc-editor.org/rfc/rfc3516.html#section-4.3 .
The mailbox raw-peer regression reproduces the old MISSING_BODY error with
SIZE + HEADER + BINARY[1]<0>, unlike the old memory server that omitted <0>.
Inventory, recovery and exact GC reads use physical BODY; only the live
streaming reader can use BINARY. APPEND and literal handling are unchanged.
