# ITOM field4 — устойчивое обслуживание DATA/control и отказ половины аккаунтов

Ревизия `transport-s1-perf5-field4`, версия `0.9.0-dev1`. Основа — точный field3; его handoff сохранён отдельно. Field4 объединяет R07 с необходимыми R13/R14 и расширяет R05/R06, включая выбранные proof/bounds-регрессии R11/R12. Результаты конкретного source ZIP, SHA-256 и все промежуточные FAIL находятся в сопроводительном `ITOM_FIELD4_REPORTS.zip`.

## Причина и изменения

При устойчивом duplex READY линии продолжали накапливать control debt и уже obsolete local DATA. Повторный обход общего ящика расходовал provider round trips перед каждым удалением; небольшие пакеты попадали на линию с заполненным descriptor/control path. Служебные очереди содержали множество заменяемых cumulative updates. При пяти доступных аккаунтах фиксированные две fast-spares оставляли только 60% линий для очистки.

Очистка теперь ограничена 32 physical attempts, 128 distinct presence UID и двумя mailbox pages на проход. Все counters выбранного presence UID проверяются вместе. Кэш хранит только живые собственные attempts двух local stores, в пределах 512 rows/16 МиБ. Уже наблюдённые eligible сообщения удаляются перед следующим scan. Для DATA небольшой cached batch дополняется bounded targeted FETCH до четырёх уже eligible attempts; если eligible rows меньше, достаточно этого количества. Будущие ACK не ожидаются, physical pass остаётся bounded32. APPENDUID сохраняется в ограниченном metadata ring (256 hints); successful hints служат поиску. Адресный FETCH сверяет текущий UIDVALIDITY и фактический MIME; прежняя цепочка Observe→physical delete proof→Deleted сохраняется. Unknown без подтверждённого APPENDUID использует обычное обнаружение. Provider без UIDPLUS сохраняет proof fallback.

Read-only presence proof работает на отдельном cleaner connection, без destructive cleanup slot и DATA guard; один gcBusy worker на lane сохраняется. Он не удаляет сообщения и не возвращает physical debt.

Pressure определяется по outgoing local debt, включая неопровергнутый старый долг. Local DATA/control/presence получают fair turns; mandatory remote cleanup не откладывается бесконечно активным owner. Local schedule имеет интервал 150 ms; physical guard освобождается до публикации completion; после завершения proof следующий уже due turn запускается без ожидания следующего actor tick. Периодические deadlines и FIFO сохраняются. Очередь admission сохраняет запрос, удаляет отменённых waiters и допускает blocked cleaner раньше временно защищённой fast-линии. При 5–10 готовых линиях сохраняются две fast-spares; при 2–4 — одна. Client spare должна иметь DATA-permit или активный fast I/O; один READY без permits недостаточен.

Для fresh 12 МиБ DATA byte share составляет 6 МиБ на incoming leases и 6 МиБ на outgoing DATA. Для existing 8 МиБ incoming share — 4,5 МиБ (прежние две bulk-leases плюс fast), outgoing — 3,5 МиБ. Bulk admission сохраняет последние 512 КиБ и один message slot для local fast DATA; incoming message share остаётся 3/4, outgoing normal — 1/4 с fast резервом. Общие journal limits неизменны. Физический серверный target выбирается одновременно с durable local grant до Seal/APPEND; точный MIME size проверяется writer. Selection получает physical writer admission до durable reservation; shutdown ждёт закрытия unused grant до unlock журнала. Pre-APPEND cancellation закрывает только unused reservations. Старый UNKNOWN/Closed debt остаётся charged до доказательства.

Служебные WINDOW/логические RECEIPT объединяются по stream/type до шифрования; обычные cumulative updates получают bounded collect10 ms, initial WINDOW и admission/liveness —1 ms, STATUS/NACK —5 ms. Уже sealed/attempted records сохраняются. GCStatus объединяется только по exact quota key и монотонным Used/Registered/Released/Closed; cleanup publish выполняется один раз на quota за проход. MailReceipt обработка линейная, ACK-before-APPEND race сохраняется. Physical MailReceipt имеет bounded 200 ms cadence. Fast-DATA исключает линии с control debt ≥80 и выбирает меньше queued control/APPEND работы; одинаковые scores сохраняют rotated fairness, owner avoidance является мягким penalty. Потоковый control выбирает менее загруженную готовую линию.

Reader audit UIDVALIDITY остаётся2 s. Fresh matching SELECT observation того же folder/account на cleaner может покрыть audit, с исходным timestamp; stale/changed/closed/foreign observations требуют reader SELECT. Этот hint не заменяет physical delete/refund proof и не добавляет connection. При reader SELECT≥100 ms cleaner после завершения physical service заранее обновляет receive observation старше500 ms; DATA guard освобождён до этого read-only audit. После periodic SELECT следующий bounded FETCH не обрезается прежним UIDNEXT: rows, пришедшие между командами, читаются сразу. Cursor advance ограничен proven allocated holes либо фактически возвращённым UID; будущие unallocated UIDs не пропускаются.

Short C2S DATA после durable wallet Spend ставит exact UsageReport в urgent очередь до APPEND, перекрывая descriptor/DATA I/O. Cumulative grant остаётся exclusive до physical completion; logical bookkeeping зарегистрирован заранее. Early enqueue failure повторяет только descriptor после единственного APPEND. Unknown effect остаётся durable debt.

## Методика приёмки

Production fixture вызывает StartClient/Serve, общий accountRuntime и настоящие TCP/TLS/IMAP sockets. Четыре upload и четыре download streams плюс DATA echo остаются теми же при owner-half/sibling-half/rotate-half outages. Payload identity независима для каждого target; useful bytes считаются после проверки. Duplicate target dial, EOF/RESET активного потока и corruption блокируют запуск.

Измерение: warmup 10 s, cadence 1 Hz, probe deadline 1 s, useful gap <5 s. Новый критерий пользователя: ≥30/30 Мбит/с при RTT max ≤600 ms; либо ≥50/50 при max <700 ms; либо ≥100/100 при max <800 ms. Один общий tier должен выполняться для обеих verified directions и завершающего окна. Для финальной проверки пользователь разрешил 10% допуска: ≥27/27 при RTT ≤660 ms, либо ≥45/45 при RTT <770 ms, либо ≥90/90 при RTT <880 ms. Включается только тестовым ITOM_LOAD_TOLERANCE_PERCENT=10; nominal acceptance логируется отдельно. Runtime, proof и deadlines не меняются. Comparisons используют исходную duration, без округления миллисекунд. Это правило применяется ко всем profiles, включая 5/10. Fault recovery измеряется отдельно с deadline 8 s, затем проводится полное steady measurement. Нулевые timeout/missed и все запланированные RTT samples обязательны.

За measurement ≥60 s проверяется последний min(60 s, половина measurement) интервал. Все фиксированные window indices обязательны; rates используют фактические наблюдённые durations. До одного cadence jitter на двух границах не считается пропущенным window. Это исправляет ошибочный FAIL при 59,9 s фактической суммы 60 полных samples; bandwidth floor сохраняется.

Обычный suite отдельно проверяет journals, proof, coalescing, concurrent local reservation, обе DB profiles и реальные lifecycle paths. Legacy loaded gates используют те же три speed/RTT alternatives и проходят повторную серию с фиксированными 32 МиБ verified payload; длительность RTT-проб больше не уменьшает workload. Exact source проверки не заменяют Windows/Android/device/provider QA.

## Совместимость и дальнейшая работа

ITH9 + LOGICAL_CONTROL: совместимо с field3, field2 отклоняется. APK/VPS обновлять одной ревизией из успешного `all`. Автоматического reset/migration и переноса physical debt между accounts нет. Governor OFF, прежние logical/account budgets и 25 s watchdog сохранены.

Следующий крупный блок: provider quotas, двухпользовательская fairness/capacity 50→80→100, dirty device/VPS upgrade и длительный soak. Затем governor shadow и отдельная приёмка регулирования. Полный plan/source history сохранены.

## Две команды

```powershell
python .\ITOM_PHASE4_SOURCE_0.9.0-dev1_transport-s1-perf5-field4.zip all --expect-revision transport-s1-perf5-field4 --accept-android-licenses
```

```bash
chmod 755 /tmp/itom-linux-amd64 && sudo /tmp/itom-linux-amd64 install
```
