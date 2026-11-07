# FIELD48 / transport-s1-perf7-field27

Исходники законченной реализации переработки P0–P4. Оба конца обновляются из одной ревизии. Android versionCode 9007, versionName 0.9.0-dev2-field27. По поручению пользователя APK и deployable server не собирались; проверка реального Android/TUN/VPS/провайдера — NOT_RUN.

## Изменения транспорта

- Единственный session-wide бюджет логических DATA bytes заменяет старые bulk permit/rate/download governors. Проверенный cumulative RECEIPT/STATUS освобождает диапазоны один раз. Residence ACK управляет допуском; Echo не используется как delivery feedback. Бюджет bulk 1–3 MiB, отдельный fast reserve 128 KiB; физический долг и лимиты journals не повышены.
- Admission предшествует sealing и journal Prepare. Offset prefixes допускаются независимо от порядка прихода workers. Готовая часть смешанной пачки публикуется, будущие элементы возвращаются owner без nonce/APPEND. Future-only batch не удерживает worker. Nonfatal retry сохраняет logical flight; stream stop освобождает только логическое место и не завершает sibling owners. Unknown APPEND сохраняет физический долг.
- Один receive command flight владеет cursor и собственным SELECT snapshot. Нет фоновых cleaner SELECT, cleaner takeover или read hedges. Четыре новых UID и до четырёх bounded gap revisits разделяют одну FETCH команду. EXISTS не вызывает повторный SELECT. UID gaps сохраняют исходную expiry при замене socket.
- EOF/reset/no-progress восстанавливают только receive role. Фактическое IO продлевает progress deadline; прикладной backpressure не считается зависанием сети. IDLE DONE имеет bounded join до замены соединения. Большой исправный BODY не прерывается по фиксированному короткому wall time.
- Общий account scheduler сохраняет MIME-byte DRR для DATA двух пользователей и paced dial/cooldown. Отдельные bounded FIFO leases обслуживают urgent, receive и maintenance. При независимых DATA/maintenance sockets короткий DATA APPEND не ждёт GC; shared-socket fallback сохраняет взаимное исключение. Quiet-reader fast reserve сохраняет приоритет и при занятом независимом control writer. Durable transactions и exact eligible BODY proofs остаются обязательными.
- TCP coalescing ограничен 5 ms, APPEND batch residence считается от возраста первого queued job. Proof cache дедуплицирует реальные BODY observations по mailbox incarnation/UID/digest/size; он не является доказательством по отправленному literal или APPENDUID. GC сохраняет fsync и пакетные durable commits. Диагностика содержит Prepare/Start/Complete timings, transaction attempts/failures/queue/commit time и reader repairs. Последний закрытый receive socket сохраняется отдельным bounded snapshot с исходной причиной отказа, даже после успешного восстановления.

Wire format, криптография, bounded replay, once-only ordered bytes, half-close и durable quota format сохранены. Отдельный control folder требует negotiation и остаётся последующим экспериментом, как было оговорено в проектном решении.

## Изменения Go проверки

Старый CPU-зависимый localhost порог 27 Мбит/с больше не блокирует сборку. Его payload integrity, bounded work, owner survival и shutdown assertions сохранены. Скоростная приёмка выделена в `model20`, где требования 5 Мбит/с и 1500 ms не ослаблены.

Shaper калибруется независимо от ITOM на двух соединениях и incompressible bytes. Лимит 20 Мбит/с общий для всех клиентских IMAP соединений в каждом направлении; full duplex и aggregate shared medium названы явно. Assumed provider service costs отделены от исторических FIELD46 wrapper-duration stress inputs. Stateful fixtures покрывают account caps, visibility, correlated queues, outages и virtual-clock boundaries.

HTTPS приёмка измеряет реально прочитанные и проверенные байты за фиксированные 60 s и последние 20 s, после явных 10 s warmup. Каждую секунду независимо запускается probe; поздние completion и ошибки учитываются полностью. Шесть заранее установленных TLS соединений исключают непреднамеренный cold OPEN/TLS из warm probes; повторное использование проверяется. Cold startup измеряется отдельно.

`model20` поддерживает single/bulk/browser (40 background HTTPS streams), service/recorded/correlated/stalls, по умолчанию три повтора, и alternating paired A/B с одинаковым candidate harness. Все результаты, включая failures, сохраняются после каждого запуска. Это модель, а не подтверждение скорости конкретного почтового сервиса.

## Проверки и локальный запуск

Точные результаты общего прогона и модели: `reports/field48/validation.json`. Исходные журналы модели и неудачных development попыток также сохранены. Матрица, не выполненная в этой среде, указана отдельно как NOT_RUN.

```powershell
python .\ITOM_TRANSPORT_FIELD48_SOURCE.zip all --expect-revision transport-s1-perf7-field27 --accept-android-licenses
```

Только standard core tests без APK/серверов: target `core`. Отдельный model20 gate: target `model20 --model-repeats 3`; browser profile: добавить `--model-case browser`. Shared-medium режим поддержан Go стендом, CLI сравнения фиксирует full-duplex профиль.

Рабочая память и исходный анализ: `TRANSPORT_REDESIGN_DECISIONS.md`, `TRANSPORT_REDESIGN_ANALYSIS_2026-10-07.md`. Состояние journals не сбрасывать при обновлении.


Финальная локальная проверка: 72 Python tests, все standard Go packages, bundled IMAP fork tests и go vet прошли; focused race — 63 test pass events в четырёх пакетах. Kotlin/native module и реальная Android/provider квалификация не выполнялись.

Один финальный 60 s duplex в модели: 7.624 Mbps upload / 5.623 Mbps download; последние 20 s — 7.445 / 5.531 Mbps; max warm HTTPS RTT 1443 ms, 60 probes, ошибок и неожиданных carrier exits 0. Предыдущий полный трёхфазный прогон не прошёл duplex RTT (2128 ms); его журнал сохранён. Финальная матрица всех фаз × 3 повтора остаётся локальной проверкой и не обозначается здесь как PASS.
