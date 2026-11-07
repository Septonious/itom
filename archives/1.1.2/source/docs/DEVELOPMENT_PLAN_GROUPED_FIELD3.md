# ITOM: объединённый план разработки

Дата: 3 октября 2026 (Europe/Moscow). Основа: field1–field3; в разработке: **transport-s1-perf5-field4**. Порядок заменяет выпуск отдельной ревизии на каждую R01–R19 из плана 2 октября. Исходные критерии корректности и приёмки сохраняются.

По запросу пользователя этапы объединены в четыре крупных блока. Каждый блок может содержать несколько внутренних изменений и Go-regressions; отдельные публичные ZIP для каждого изменения не нужны. Red→green evidence и причинное объяснение сохраняются внутри одного выпуска. Если тест обнаруживает новый продуктовый сбой, он остаётся в отчёте, а не объявляется успешно закрытым.

| Блок | Исходные этапы | Результат |
|---|---|---|
| 1. Состояние и достоверный стенд | R01–R05; cancellation-часть R15 | Совместимость old DB, атомарная миграция, уникальные payloads, строгий EOF, независимые measurements, реальные supervisors |
| 2. Прогресс и восстановление | R06–R10; обусловленные ими изменения R13/R14 | Causal wait chain, устранение sustained stalls, logical owner/control вне carrier lifetime, безопасный repeated rejoin |
| 3. Корректность и нагрузка | R11/R12, оставшиеся R13–R17, Q01–Q03 | Delete proof, byte/RAM budgets, quotas/provider model, fairness, sustained 50→80→100 |
| 4. Android и выпуск | Q04–Q06; R18/R19 после qualified baseline | Windows all, APK/VPS pair, dirty upgrade/device/soak; governor shadow→bounded regulation с повторной приёмкой |

## Блок 1 — результат field2

Реализовано:

- R01: exact allowlist DATA 8 МиБ/128 и 12 МиБ/192; сохранённый профиль не повышается при open; control остаётся strict 2 МиБ/128. Provider/account/user/folders и metadata caps сверяются полностью.
- R02: явная offline миграция профиля, audit прежнего/нового budgets, атомарные config+versioned audit record; идемпотентность; process-death до/после commit; потерянный ACK; отказ небезопасного downgrade. Миграция не включается автоматически.
- T01: настоящие файлы DB созданы исходным gate19 binary/test code; пустая и dirty DB с Prepared/Started/Unknown/observed и закрытым remote grant. Identity, balances, grants, references, digests/locations и fencing проверяются после двух reopen и миграции.
- R03: ожидаемая session/user/direction/stream identity задаётся независимо для каждого TCP target port. Cross-stream swap и corruption выявляются. Активный EOF/RESET и target write failures передаются controller; полезные bytes считаются после verify.
- R04: отдельный 1 Hz probe каждого user, независимые fixed 1/5-second windows, nearest-rank, max/timeout/missed; measurement-end summary до drain; bounded failure timeline/stacks/debt; явные фазы и deadlines. Самpler обнаруживает 5 секунд отсутствия useful progress независимо от 25-секундного transport watchdog.
- R05: fault fixture вызывает `StartClientWithOptions` и общий механизм `Serve`, включая shared account DATA workers, recovery, janitor, retries и epochs. Injected dial использует настоящие TCP/TLS/IMAP sockets. Normal supervisor lifecycle входит в обычный Go suite; product fault gates вызываются явно.
- R15 (частично): Close прерывает pacing тестового provider. Cap-update requeue/deadline semantics и provider quotas остаются в блоке 3.
- Исправлен launcher: пропуск только трёх явно названных opt-in qualification tests отображается как skipped_optional. Любой их FAIL и любой пропуск mandatory test по-прежнему блокирует сборку.

Блок 1 завершает инфраструктуру проверки; он **не закрывает** блоки 2–4. В field2 сохранены ITH9/capabilities, governor OFF, окна/writers/таймеры и exposure fresh-state 12/192. Existing 8/128 работает с собственным fast reserve.

## Блок 2 — field3 и оставшаяся sustained-приёмка

1. На 2/4 all-owner accounts добавить bounded correlated debt/grant timeline (R06). Имеющийся 4a/60с run field2: 25,297/47,339 Мбит/с, max RTT 750 мс. Это воспроизведение недостаточного sustained профиля, не новая квалификация ≥30/30/<700.
2. Выбрать доказанную причину и исправить wait/cleanup/usable grant chain. Closed не usable; Unknown остаётся charged; no refund по receipt/TTL.
3. Отделить logical owner, Pump/TCPExit и flow/control routing от carrier context (R08/R09). Сохранить 4 МиБ/64/4096 scopes или явно доказать эквивалентный bounded scope. Если нужны новые wire semantics, ввести negotiated capability и согласованный APK/VPS upgrade.
4. Исправить repeated non-owner failure и owner-loss reproducer. Первый non-owner run прошёл, последующие ordinary/race runs дали closed pipe/timeout после второго outage. Owner-loss воспроизводится как FAIL. Возможная pre-APPEND cancellation→fatal Pump completion остаётся гипотезой, не готовым исправлением.
5. Account rejoin не переносит physical debt и не повторяет TCP target dial. Проверить 2/5-second outages, ambiguous APPEND, обе стороны, repeated loss, dirty journals и late epochs.

Приёмка блока (уточнена пользователем): соединение сохраняется при наличии как минимум 50% рабочих аккаунтов — 5 из 10. Сценарий 2 из 10 не блокирует выпуск. Проверяются потеря owner-половины, sibling-половины и смена доступной половины; те же sockets/session/generation и exact bytes, без второго target dial. Fixed measurement ≥60с, useful gap <5с; no-fault production ≥30/30 и RTT max <700; old heavy gates 5/5. Для прогона ≥60с тот же bandwidth floor обязателен и в завершающем окне, чтобы начальный burst не скрывал деградацию. Профили иных provider условий задаются явно в отчёте. Diagnostics OFF — скорость, ON — causal evidence. Все failed runs сохраняются.

## Блок 3 — следующий объединённый нагрузочный выпуск

- Проверить concurrent unflag/partial STORE/EXPUNGE/UIDVALIDITY и отсутствие proof refund до доказанного удаления (R11).
- Ввести проверяемые byte/RAM bounds для caches и service cleanup; регрессии exhausted bytes/messages, misleading ready spare, expired collector и release-before-done (R12–R14).
- Довести cancellation/deadline semantics provider model. Отдельно измерить ingress/egress MIME/TLS и useful bytes; общий cap 100 Мбит/с не означает useful 100/100.
- Измерить реальные quotas, общий account role budget и recovery после quota rejection (R15/R16,Q01/Q02).
- A-only/B-only/A-bulk+B-interactive/оба bulk, асимметричные batches. Менять RR на cost/DRR только после доказанного unfairness (R17).
- Ступени 50/50→80/80→100/100: warmup 10с, measurement ≥60с, пять последовательных qualification runs; per-user RTT/bytes, fixed windows/gaps и bounded debt. 100/100 у обоих — отдельная 200/200 aggregate квалификация.

Приёмка: proof/bounds корректны; runtime не компенсирует недостаточный provider cap увеличением долга; ≥100/100 sustained при доказанной capacity, synthetic no-fault RTT <700 и product max <1000 мс. Single-stream throughput публикуется отдельно.

## Блок 4 — выпуск и governor

- Windows `all`: фактические Kotlin tests, native bridge, Gradle/lint/APK и Linux binary одной ревизии. Linux source/native PASS не заменяет APK/device.
- Устройство: fresh+dirty upgrade, idle→bulk, screen off, Wi-Fi/mobile, DNS/protect(fd), процесс/сеть/outages.
- Installer: existing state, transport readiness и совместимый rollback; systemd active недостаточно.
- Soak 30–60 минут, затем 6–12 часов, без монотонного роста debt age/RAM/queues/connections.
- Governor после блоков 2/3: shadow без влияния, затем bounded bulk regulation. Target = начальный authenticated RTT+400 мс; feasible useful floor 10 Мбит/с, hysteresis 9–11; control/fast/cleanup сохраняют service.

Можно выпустить квалифицированный baseline без governor и включить его после shadow приёмки отдельным внутренним изменением. Реальные provider/device проверки нельзя заменить Go-тестами.

## Выпуски и внутренние проверки

Один ZIP на крупный завершённый блок, а не на каждый R-пункт. Внутри: focused tests для каждого изменения → общий non-heavy/race/native/vet → old heavy → exact production/fault → длительная серия. Широкие проверки повторяются после material change; результаты не выбираются по лучшему run.

Источники: приложенные полный анализ и план 2 октября, field1 handoff и exact assets. Исторический план 1 октября используется для ограничений/методики, а не как новая очередь от gate18.


## Состояние после field3

Объединены R06 и основной lifetime/rejoin код R08–R10: correlated actor
snapshots, самостоятельный logical owner, новая exact capability, безопасный
NotCommitted retry и accounting recovery своего/чужого DATA. Повторный rejoin
использует тот же actor и бюджет. R07 и loaded fault/sustained приёмка блока 2
ещё не закрыты: 2/4a диагностические прогоны выявили probe timeout, а admission/
cleanup candidate не прошёл. Он сохранён disabled и исключён из runtime.
Следующая разработка объединяет R07 с причинно связанными R13/R14 блока 3;
блок 2 нельзя считать полностью принятым по одному успешному echo fault gate.
Каждый релиз заканчивается двумя командами: `all` сборка source ZIP, затем
`chmod 755 /tmp/itom-linux-amd64 && sudo /tmp/itom-linux-amd64 install`.
