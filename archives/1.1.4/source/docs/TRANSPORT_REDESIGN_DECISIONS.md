# Рабочая память проекта: transport rewrite

Зафиксировано 7 октября 2026 по прямому указанию пользователя. Исходная база: FIELD46 / `transport-s1-perf5-field25`. Полный принятый разбор: `TRANSPORT_REDESIGN_ANALYSIS_2026-10-07.md`; входы воспроизведения: `fixtures/field46-replay-inputs.json`.

## Обязательный контракт

- Для общего физического доступа 20 Мбит/с: полезная скорость каждого активного направления не менее **5 Мбит/с**, как за всё измерение, так и в последней трети. Максимальный прикладной RTT **не более 1500 мс**, включая ошибочные/поздние пробы. Допуска 10% нет.
- Разработка: точечные проверки изменённого поведения. Перед упаковкой кандидата: один общий прогон стандартных проверок. Локальные модельные результаты и Android/VPS/реальный провайдер указываются отдельно; отсутствие полевой проверки — `NOT_RUN`.
- Сохраняются криптография, упорядоченная однократная выдача байтов, half-close, физическая квота, durable Start до APPEND, долг при unknown, фактическое BODY-доказательство до удаления/refund, fsync и защита сокетов Android от VPN-петли.
- Нельзя лечить регрессию понижением критерия, бесконтрольным увеличением сокетов или автоматической перепубликацией unknown APPEND.

## Архитектура

Логический session/stream owner сохраняет offsets, окна и единственный terminal/retry budget независимо от carrier. Планирование аккаунта ограничивает байты и команды, разделяет классы работ, обеспечивает справедливость между пользователями и общий paced dial/cooldown. Каждый APPEND-worker владеет одним сериализованным сокетом и явным исходом not-started/accepted/unknown. Receive-worker имеет единственный command flight и cursor; cleaner не используется как скрытый резервный reader. Proof/GC остаётся отдельным ограниченным сервисом с атомарными batch commits.

First-close-cause, socket generation, role, idle/IO age и стадия операции записываются безопасными категориями без credentials, literal body и hostname назначения. Уже замеченный EOF не превращается в новый unknown APPEND; ошибка после возможной записи сохраняет unknown. Замена DATA-сокета не завершает здоровый логический stream.

Управляющая работа должна зависеть от действительного progress/recovery; idle STATUS и неограниченные terminal retries убираются. TCP/APPEND coalescing имеет абсолютный deadline; существующие wire limits сохраняются. Отдельный control-folder требует negotiation и входит в последующий эксперимент, а не добавляется автоматически.

## Доказанные данные и ограничения

FIELD25: download около 5.8–6 Мбит/с, upload 30–50.9 Мбит/с; 15 немедленных unknown/socket_closed в активной сессии. Первая причина закрытия не записана. 91.25% APPEND по количеству — control, это не доля байтов. Есть bulk queue age до 7.65 с, частые пустые FETCH и конкуренция cleaner/reader. 53 OPEN_ERROR timeout соответствуют 15-секундному target dial, а не доказанному отказу IMAP. Часы клиента и сервера не синхронизированы. Массовые поздние rejoin после session_stop не объясняют предыдущий speedtest.

Maxu46 — прямой TCP с IMAP-мимикрией, не сравнимый mailbox carrier. true-imap-tunnel полезен разделением sender/watcher и affinity; его blind APPEND retry нельзя переносить в наш quota contract. White-Shield полезен bounded batching, account pacing и polling; его опубликованные скорости не являются гарантией ITOM. Реализация здесь самостоятельная, без копирования чужого исходника.

## Порядок внедрения и принятие

Checkpoint FIELD47 / `transport-s1-perf6-field26`: первый этап реализован, стандартная сборка и проверки PASS. Последний синтетический model20 FAIL: duplex download последней трети 3.631 Мбит/с, app RTT 1355 мс; оба прогона сохранены в `reports/field47`. Это экспериментальный кандидат; полевая проверка NOT_RUN. Продолжать с delivery governor/window и receive recovery по прогрессу. Не считать устранённой реальную регрессию по успешной сборке.

Сначала lifecycle/account admission/receive isolation и воспроизведение faults, затем control/terminal budget, фиксированное окно измерения и полезные байты. Далее delivery feedback, paired A/B и реальный Android/TUN/VPS/provider gate. Experimental source candidate может содержать явный qualification FAIL/NOT_RUN; стабильным релизом он от этого не становится.


## Действующее поручение и реализация FIELD48

Пользователь потребовал завершить всю переработку до отправки, без промежуточных релизов. Сборку APK/серверов он выполняет локально. Эта инструкция заменяет прежний порядок промежуточных поставок. В разработке — точечные проверки; перед архивом — общий стандартный test-only прогон. Полевую скорость без реальных измерений не объявлять подтверждённой.

P0–P4 реализованы за существующими wire/journal интерфейсами. Единственный delivery controller учитывает проверенные cumulative RECEIPT/STATUS ACK логических байтов, фиксирует порядок допуска offsets и допускает только готовую часть смешанной пачки: будущие/непомещающиеся элементы возвращаются owner до sealing/Prepare. Definitive nonfatal yield сохраняет логическое место до retry/stop; unknown сохраняет физический долг. При остановке stream его ожидающая пачка не завершает соседних owners.

Receive имеет один command flight, переносит bounded UID gaps с исходными expiry, восстанавливает EOF/reset/no-progress только своего socket и не использует cleaner. DONE имеет отдельный bounded join; чтение большого BODY продлевается реальным IO progress, прикладной backpressure не считается сетевым зависанием. Account command classes имеют FIFO и отдельные ограниченные leases; DATA сохраняет общий MIME-byte DRR. Proof cache дедуплицирует действительные BODY observations по folder/UIDVALIDITY/UID/digest/size, не заменяя proof отправленным literal или APPENDUID. Добавлены отдельные Prepare/Start/Complete timings и счётчики durable commits.

Стенд различает предположенные service costs и исторические wrapper-duration stress samples, калибрует общий shaper независимо от ITOM, включает stateful connection/command caps, visibility, account-correlated queue delay и virtual-clock state tests. Приёмка считает фиксированные полезные байты, таймовой хвост и все probes, включая поздние/ошибочные; модель поддерживает single/bulk/browser и парные повторные запуски. Подробные результаты каждого запуска, включая найденные deadlocks и исправления, сохраняются в `reports/field48`.

Отдельный control-folder остаётся заранее оговорённым negotiation экспериментом, а не незавершённой частью этой реализации. P5 — локальная сборка и полевые Android/TUN/VPS/provider измерения пользователя — NOT_RUN в данной среде.

Дополнение финальной реализации: receive не выполняет SELECT после каждого EXISTS; UIDVALIDITY обновляется по собственному ограниченному расписанию. Страница ограничена четырьмя новыми UID и четырьмя gap revisits, чтобы амортизировать RTT без неограниченного FETCH. При независимых DATA/maintenance ролях GC не исключает короткий DATA APPEND: журнал и точные eligible BODY proofs остаются границей безопасности; при общем сокете действует прежнее взаимное исключение. Признак изоляции фиксируется до публикации carrier и не меняется посреди service lease.

Контроллер учитывает residence до authenticated logical receipt, а не утверждает потребление приложением за удалённым TCP сокетом. Полезная скорость и RTT приёмки считаются отдельно на реально прочитанных и проверенных HTTPS байтах. Совместимый диагностический DTO сохраняет исторические имена, но удалённые rate/bulk/download governors не работают параллельно.

Исправление методики: запуск probe каждую секунду при RTT >1 с создаёт перекрывающиеся HTTP/1 запросы. Прогрев одного TLS соединения недостаточен: следующая проба могла включать новый OPEN+TLS. Стенд заранее устанавливает шесть отдельных TLS соединений, чередует их с интервалом 6 с при timeout 5 с и проверяет GotConn.Reused для каждой loaded probe. Все реальные ошибки/поздние probes сохраняются; cold OPEN/TLS имеет отдельные измерения. Старые development попытки с недостаточным прогревом сохраняются как FAIL, их maximum нельзя выдавать за чистый warm RTT или прямое финальное A/B сравнение.


## Финальная фиксация FIELD48

Quiet reader остаётся предпочтительным для короткого DATA при независимом control APPEND: занятость другого socket больше не отправляет HTTPS request в очередь bulk BODY. Последний failed receive generation сохраняется в диагностике с first-close reason.

Финальный 60 s duplex после этой правки: upload 7.624 / download 5.623 Mbps, последние 20 s 7.445 / 5.531 Mbps, максимум warm HTTPS RTT 1443 ms, 60 probes, ошибок и неожиданных carrier exits 0. Это один синтетический прогон, без квалификации Android/реального провайдера. После измерения добавлена только сохранность failed-reader диагностики и исправлены тестовые ожидания/барьер; performance path не менялся. Общая матрица повторов и paired A/B выполняется локально.

Стандартный прогон обнаружил два дефекта тестовой обвязки: ожидание worker перед освобождением его barrier и прежнее ожидание 5 ms для empty hot poll. Первый исправлен ожиданием входа в blocked operation с последующим release; второй проверяет всю последовательность 10/20/40/80 ms. Неудачные журналы сохранены, приёмочные 5 Mbps / 1500 ms не ослаблены.


## FIELD49: новые полевые данные и исправления

FIELD48 не удовлетворил пользователя: обычный speed test не работает, idle RTT около 500 ms. Model PASS не является подтверждением решения этой проблемы. Анализ: FIELD48_LIVE_ANALYSIS_2026-10-08.md.

Исключить NACK-to-repair для definitively unsent Queued item: original send ещё не опубликован. Event-driven deferred admission не удерживает worker и не создаёт миллисекундный retry loop. Fresh client short request primes только quiet receive reserve и прерывает empty poll; replies/repair не инициируют reciprocal hot state. Native copy stage диагностируется отдельно. Сохраняются user thresholds 5 Mbps / 1500 ms; неудачные model runs не скрывать. Сборка APK/серверов остаётся локальной.


## FIELD50 — уточнение сети и конкурентные OPEN (2026-10-08)

Пользователь: все полевые скриншоты получены на стабильной сети 130 Мбит/с. Не объяснять их ограничением 20 Мбит/с. Обычный спидтест сразу завершается ошибкой; гипотеза пользователя — множество одновременных TCP. Причина приложения пока не установлена; начало traces вытеснено. Сохранённые 21 OPEN TCP8080 успешны. Native128/owner64 limits не доказаны как причина реального отказа.

96 concurrent OPEN/bytes/FIN через production supervisors проходят уже с FIELD49. Два новых owner reservation regression воспроизводят недостаток FIELD49: pending OPEN не учитываются, заполненный быстрый owner выбирается при свободном соседе. FIELD50 резервирует под Client.mu, считает routes до terminal retirement и не расширяет лимиты. Полный owner отклоняет только новый socket, carrier reconnect сохраняет тот же budget.

Модель130 введена отдельно от model20: calibrated130/assumed250ms path/provider service, 16 bulk streams в направлении +24 background workers, 6 warm HTTP connections и отдельные idle observations. Её результаты — measurement-only. Не выдавать CPU-dependent localhost smoke, 20 Mbps qualification или один synthetic duplex за реальное улучшение. Полная матрица по прежнему доступна локально. Обе границы 20 Mbps5/1500 сохраняются.

В полевом server retained bulk health и первоначальной130 модели окно1MiB остаётся на минимуме; residence превышает fixed800ms. Новый ACK floor учится только из original low-flight (<=512KiB) ranges, требует4 observations в кольце8, capped900ms, target800..1200ms и window1..3MiB. Repair исключается, unknown debt/physicalproof не меняются. Оценка не является основанием утверждать улучшение idle; фактические результаты и development attempts сохранять, включая отсутствие убедительного ускорения.

Проверки точечные в разработке, единый полный standard core sweep перед упаковкой. APK/AAR/deployable server не собирать. Native bridge attempt блокируется отсутствующими pinned dependencies в локальном offline cache; не объявлять gVisor/TUN проверенным. Обновлять оба конца одной ревизией, journals не сбрасывать. Детали и результаты: FIELD50_130M_CONCURRENCY_ANALYSIS.md и reports/field50/validation.json.

Дополнительный regression FIELD50: отмена после server dial до OPEN_OK оставляет server1/client0 route на FIELD49; FIELD50 принимает поздний результат и закрывает app socket. После actor acceptance route/capacity освобождает actor, не caller; pre-submit cancellation освобождается сразу. OPEN_ERROR освобождает route, Client.Open error возвращает настоящий nil Stream. Этот дефект не объявлять доказанной причиной реального отказа приложения.


## FIELD51 / transport-s1-perf7-field30 — продолжение 8 октября 2026

Полевые screenshots относятся к стабильной130Mbps сети. Не считать Go localhost smoke доказательством полевой скорости, не снижать gate20: каждая активная useful direction и tail >=5Mbps, max appRTT<=1500ms, ошибки0. APK/AAR/deployable server пользователь собирает локально.

Воспроизведена потеря bulk demand между вызовами maintain в пределах1s epoch: periodic maintainer видит пустую очередь waiter после парковки DATA jobs и сбрасывает полезные ACK samples без роста окна. FIELD51 запоминает bulk admission demand до следующего решения, consumes его один раз; fast/repair waiters не создают bulk demand. Stall shrink, idle/no-demand и1..3MiB bounds сохранены.

Эксперимент receive tail4→8→16UID не входит в FIELD51: в одном130Mbps сравнении download16.930 против14.527Mbps вырос, но upload13.251 против19.905 и duplex9.712up/11.211down против10.783up/16.244down были хуже. Это не доказательство причинности, однако публиковать как ускорение нельзя. Patch и raw evidence сохранены в reports/field51/development_attempts. Финальные модели повторяются на demand-only runtime; production FETCH остаётся4UID.

Один baseline FIELD50 и final candidate проверяются в одинаковом service/access250ms/130Mbps сценарии по download/upload/duplex,60s phase, seed1. Данные промежуточного эксперимента не квалифицируют final runtime. Один последовательный A/B не является статистической квалификацией и не воспроизводит Android/gVisor или реальный provider. Не утверждать устранение отказа обычного Speedtest без раннего field trace. Результаты и полный core gate см. reports/field51/validation.json и RELEASE_FIELD51.md.

Перед упаковкой обнаружен второй tradeoff: demand-only130 upload26.525Mbps, duplex25.974up/13.190down, но maxwarm HTTP RTT2967ms. Эти данные не являются final release qualification. Добавлен bounded fast DATA ACK pressure feedback (1500ms outstanding/complete residence,2s hold, bulk shrink1/8 per1s epoch, no debt bypass). Поэтому финальные модели и core gate повторяются на guard runtime; raw прежних попыток сохранён. Нельзя утверждать снижение actual idle или исправление обычного Speedtest по этим моделям.

Первый guard20 run имел maxappRTT1508ms вduplex: FAIL без допуска. Добавлен отдельный generation свежих reader response hints, который сбрасывает empty streak; bulk/GC wake не меняет pacing. Regression до исправления140.465ms для100ms command плюс40ms унаследованной паузы; новый reader начинает с10ms, steady cap80ms сохранён. Все предыдущие данные guard/demand/wide оставлены как development, final models/core fingerprint проверяются повторно.

После reader fix20 download ещё имел max1531ms: FAIL. Final feedback trigger выбран1000ms DATA ACK residence, чтобы оставить запас для второй leg; это не предел appRTT. Min bulk budget уменьшен с1MiB до768KiB (initial1MiB/max3MiB), позволяя разгрузить очередь дальше при short pressure. Все ACK/debt invariants и exact5/1500 gate сохраняются; новая headroom test проверяет снижение ниже initial без освобождения bytes. Новые qualified runs имеют отдельные source hashes; старые FAIL сохранены.

## FIELD51 continuation after environment reconnect

- Last pre-reconnect runtime7fccc failed strict20 gate:duplex down6.838/up7.008Mbps,maxRTT1595ms. No allowance.
- Functional regression on FIELD50 selects bulk a03 while quiet readers have one free physical slot and independent control APPEND. Fix preserves quiet preference until the effective slot limit is reached; hard capacity is unchanged.
- Targeted race run33 test events PASS; full20/130 models and final core gate are separate evidence. Never infer real Android/provider performance from these synthetic runs.

## FIELD52 / transport-s1-perf7-field31 — guarded capacity pilot (2026-10-09)

User reported real FIELD51 Speedtest below 20Mbps on raw130Mbps with RTT ~500ms even idle. FIELD51 remains minimal stable; speed vs RTT must be compared on Android and VPS. New capacity probe is a cautious, capped +128KiB trial at useful-rate plateau with short DATA latency rollback and measured ACK throughput validation; no unconditional 2MiB initial flight. Current environment only validated pure controller Go 1.23 tests and Python offline source suite. Full Go1.27/model20/model130/Android/device gates NOT_RUN. See RELEASE_FIELD52.md.

## FIELD53 / transport-s1-perf7-field32 — RTT guard + fast ramp (2026-10-09)

Реальный FIELD52 Speedtest: 22.5/25.9Mbps, ping idle384ms, loaded download371ms, upload379ms. Client trace 22:26–22:29 shows all ten ProbeAttempts=0, so FIELD52 plateau pilot did not cause observed speed gains; обычное additive window выросло до1361920bytes. Добавлен fastRamp guarded acceleration только при useful ACK >=16Mbps, recent short ACK <=525ms, bulk ACK residence с headroom и достаточным спросом. 192KiB vs обычные64KiB рост за epoch; rollback when pending fast >600ms or fresh ACK >baseline+110ms; 18s cooldown. Min768KiB/max3MiB,128KiB fast-reserve, worker counts, wire/quota/UNKNOWN APPEND debt preserved. 11 isolated controller tests normal/race PASS; полноценная Go1.27/model20/model130/Android qualification NOT_RUN. FIELD52 — baseline для полевой скорости, FIELD51 — минимально рабочая база. Не выпускать production как доказанный40/40 до полевого подтверждения.
