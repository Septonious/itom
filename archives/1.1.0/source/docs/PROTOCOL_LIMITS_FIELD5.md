# Физические ограничения и реалистичная приёмка field5

Пользователь разрешил ослаблять требования, если они превышают теоретические возможности. Это не разрешение скрывать implementation bugs или regression.

## Полезная скорость

MIME encoding: base64 4/3 и CRLF каждые76 символов. Без headers/TLS/Noise/proofs полезная верхняя граница `C*3/4*76/78 = 0.730769*C`. При50Mbps невозможно60 полезных; при10Mbps максимум7.308 полезных ещё до control/GC/loss.

С текущим batch collector оптимистичный payload одного APPEND не больше336KiB+32KiB=368KiB. На account2 DATA writers; каждый APPEND подтверждается до следующего на том же writer. Для4 active stream owners суммарный Pump capacity32, поэтому10accounts ограничивают20 физическими writers. БезLITERAL+ большой literal>4096 bytes требует continuation handshake + completion, то есть минимум2*baseline RTT. С advertisedLITERAL+ — минимум1*baseline RTT. Stop-and-wait uplink ceiling: `writers * 368KiB * 8 / (appendTurns * baselineSeconds)`; duplex не выше min(wire ceiling,uplink ceiling).

| 10/10,4 owners,C600Mbps | LITERAL- upper | LITERAL+ upper |
|---|---:|---:|
| baseline100ms |301.465Mbps|438.462Mbps (wire cap)|
| baseline200ms |150.733Mbps|301.465Mbps|
| baseline300ms |100.488Mbps|200.977Mbps|

Это оптимистичные bounds, а не прогноз и не SLA. Размер реально сформированной пачки, shared CPU, headers/crypto, provider latency, grant/control traffic и verified physical GC снижают скорость. Снижение обязательных60/30 допускается только когда upper bound уже ниже nominal target; вместо невозможного абсолютного порога проверяются прогресс/integrity и paired efficiency against field4. Приемлемый пользователю уровень125+Mbps оценивается отдельно от минимальных60/30: приLITERAL-/300ms теоретический uplink ceiling уже ниже125; такой профиль требует оценки эффективности и устойчивости. ПриLITERAL+/100–300ms ceiling выше125, однако сам по себе не доказывает достижимость sustained125 после всех расходов.150 — ориентир,200 отложено; стабильность важнее расширения capacity.

Обнаруженный библиотечный defect (LITERAL+ не использовался) исправлен вместо объявления его ограничением протокола. Calibration100ms/600Mbps/0loss: large32KiB minus207.3ms, plus105.7ms; small100B ~101ms в обоих режимах. Тест также сохраняет mandatory synchronizing fallback приLITERAL-.

## RTT

Ping сети не равен application echo через mailbox. Для cold IDLE reply сначала приходит notify, затем DONE иFETCH. Даже при overlapped DONE/FETCH нужны два сетевых RTT до получения response body; SELECT/provider work и queues добавляют время. Поэтому gate имеет floor2*baseline+150ms, max с прежним baseline+max(150ms,3ms*currentBandwidth), затем10% test allowance. Нельзя обосновывать лишнее ожидание tagged DONE, reader backlog или protocol outage этой формулой. Абсолютный cap1000ms сохраняется для loss=0. По уточнению пользователя от2026-10-04 при loss>0 применяется отдельный hard cap3000ms, floor25Mbps (22.5 с10% люфтом).

Loss0–5% на reliableTCP не означает потерю DATA на уровне приложения. Повторная передача может дать хвост задержек: RFC6298 предусматривает RTO и backoff. Нельзя гарантировать жёсткий worst-case1000ms для произвольной последовательности network drops. Однако текущая Go-модель не реализует kernelTCP congestion control/RTO; её results нельзя выдавать за реальные packet-drop tests. Решение о дальнейшем conditional tail SLA для loss profiles принимается по независимо откалиброванному стенду и paired profiles, не под конкретный неудачный seed. Corruption, scope/nonce errors, spontaneous account outage и regression остаются blockers.

References: RFC2045 https://www.rfc-editor.org/rfc/rfc2045.html; RFC7888 https://www.rfc-editor.org/rfc/rfc7888.html; RFC2177 https://www.rfc-editor.org/rfc/rfc2177.html; RFC6298 https://www.rfc-editor.org/rfc/rfc6298.html.

## Минимум половина рабочих аккаунтов


Текущая квалификация проверяет минимум5/10 аккаунтов; состояния ниже половины не моделируются. Историческая severe-loss unloaded диагностика ранее давала хвост около2s при baseline300ms, поэтому новый3s предел отражает retransmission latency, а не скорость125Mbps. Это не гарантия real TCP worst case.
