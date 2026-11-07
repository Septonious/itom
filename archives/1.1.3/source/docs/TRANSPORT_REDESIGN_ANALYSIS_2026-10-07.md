# ITOM: разбор FIELD46 и проект переработки транспорта

Дата анализа: 7 октября 2026. Проверяемый runtime: `transport-s1-perf5-field25`, Android `0.9.0-dev2-field25-debug`. Это архитектурный разбор и спецификация следующего этапа; новый runtime, APK и сервер в этом комплекте не выпускаются.

## Решение

Нужны новое транспортное ядро и новый стенд нагрузочной проверки. Продолжать настройку нескольких взаимосвязанных ограничителей по результату localhost-теста недостаточно. Переработка должна устранить размножение управляющей работы и отделить логический поток от почтовых подключений, а тесты — воспроизводить наблюдавшиеся отказы.

Это не означает выбросить все доказанные свойства. Сохраняются криптография, проверка границ записей, однократная упорядоченная выдача байтов, ограничение физических долгов, доказательные правила очистки, защита сокетов Android от возврата в VPN и проверки отмены/перезапуска. Транспортная оркестрация, планировщик, обратная связь, модель провайдера и критерии сборки проектируются заново. Старые unit-тесты сохраняются там, где проверяют эти свойства; тесты, закрепляющие прежние эвристики, заменяются проверками поведения.

Причина низкой скорости пока не сведена к одному доказанному дефекту. Подтверждены несколько затрат и отказов; точный вклад каждого требует воспроизводимого сравнения. Особенно нельзя объявить установленной причину закрытия DATA-сокетов: нужной телеметрии нет.

## 1. Что исследовано и где заканчиваются данные

Исследованы два новых диагностических архива, оба скриншота, текущие исходники FIELD46, FIELD44–46 отчёты, Windows FIELD45 FIX1 `core-report`, `core-build` и `core-test-events`, а также исходники трёх указанных проектов на закреплённых commits. Исторические отчёты — свидетельства соответствующих запусков, а не новая проверка всех прошлых сборок.

| Источник | Подтверждённый интервал/версия | Ограничение |
|---|---|---|
| Клиентская трасса | 18:52:31.850–18:57:28.605 UTC; 150 745 записей; FIELD25 в diagnostics | Начало сессии вытеснено ротацией; итоговый diagnostics снят в состоянии OFF |
| Серверная трасса | 18:52:22.909–18:58:05.252 UTC; 171 362 записи; FIELD25 в capture | Начало сессии также отсутствует |
| Cloudflare | 21:56:52 на скриншоте | Download 5.83, upload 30 Мбит/с; latency 455, jitter 225 мс |
| Ookla | файл скриншота 21:52:21 | Download 5.98, upload 50.9 Мбит/с; ping 332; loaded down 481, up 1175; jitter 99 мс |
| Windows FIELD45 FIX1 | FIELD24, Go 1.27.1 windows/amd64 | Локальная IMAP-модель: 25.373117 Мбит/с / 566.6589 мс; сборка остановилась на performance gate |
| Linux FIELD45/FIELD46 | FIELD24 / FIELD25 | Локальные smoke и отдельные mobile model результаты; не Android↔провайдер↔VPS |

В каждой сохранённой трассе положительные `seq` непрерывны: клиент 122658–273401, сервер 125228–296589. В health-снимках `Dropped=0`, `WriteErrors=0`. Это подтверждает целостность сохранённого суффикса, а не наличие всей сессии. Клиентская завершающая запись `trace_stop` имеет seq=0 и учитывается отдельно.

Есть десять наблюдаемых аккаунтов: a01, a02, a04–a11; пропуск a03 не означает отказ одиннадцатой линии. Их идентификаторы не заменяются в анализе произвольными a01–a10.

Первый скриншот Ookla предшествует началу сохранённой клиентской трассы. Его нельзя точно связать с отдельными очередями и командами. Cloudflare находится внутри сохранённого интервала. Часы сторон не синхронизированы в доказательствах: встречается отрицательная разность server RX − client TX для одного ciphertext. При предположении постоянного смещения и неотрицательной доставки парные сообщения допускают client-clock − server-clock примерно 505–636 мс. Это оценка границ, не калибровка часов. Поэтому сырые межмашинные разности не выдаются за RTT; длительности операций и ожиданий внутри одной машины пригодны для анализа.

Архивы не содержат прямого замера доступа без ITOM, достоверной скорости физической линии во время каждого спидтеста, полных endpoint-параметров или парных реальных запусков прежнего и нового APK в одинаковых условиях. Поэтому количественную величину регрессии между релизами установить нельзя. Низкий download, асимметрия, ошибки DATA и большие задержки очередей подтверждены независимо от этого ограничения. Два текущих скриншота сами по себе не являются сравнением разных релизов.

## 2. Подтверждённые наблюдения

### 2.1. DATA-подключения закрываются в рабочей сессии

Клиент: 7 `append_data / IMAP_APPEND_OUTCOME_UNKNOWN / socket_closed`, длительности 0.074–0.837 мс. Каждое сопровождается снижением DATA capacity 2→1 и последующим восстановлением 1→2. Затронуты a04, a06, a09, a10, a11.

Сервер: 8 таких ошибок, 0.064–2.465 мс; затронуты a01, a05, a06, a08, a09, a10, a11. Общий account DATA runtime скрыт за интерфейсом lane, поэтому событие нельзя надёжно привязать к одному физическому worker из имеющихся полей.

Примеры времени UTC: сервер 18:54:07.349 a08; 18:54:11.574 a05; клиент 18:55:39.617 a06; 18:56:32.941 a04; сервер 18:56:50.248 a01. Эти ошибки произошли до завершения клиента.

Такая малая длительность показывает обнаружение уже закрытого/непригодного соединения или немедленное закрытие при записи, а не ожидание 30-секундного APPEND timeout в этой операции. Неизвестно, что закрыло сокет: IMAP BYE/EOF, серверный лимит, таймер простоя, сеть, отмена или ошибка клиентского decoder. В трассе нет socket_id, worker_id, последнего успешного IO, оригинальной ошибки reader и first-close-cause. Ошибка `socket_closed` — следствие, а не установленная причина.

Исходник `laneMail.AppendData` уже изолирует неисправный writer и асинхронно восстанавливает его; серверный `accountRuntime.runWorker` также закрывает worker после ошибки. Следовательно, предложение «добавить переподключение writer» повторяло бы имеющийся механизм. Нужны диагностика первого закрытия, управление простаивающими sockets и общий account-level cooldown с учётом вида отказа.

### 2.2. Массовые rejoin после остановки — другой класс событий

Клиент в 18:57:28.598 записал `underlying_network/session_stop`, затем отмену IMAP-операций и `trace_stop`. В 18:57:51.982–18:57:53.169 сервер переподключил девять линий с `TRANSPORT_TIMEOUT`, затем вошёл в recovery/handshake. Ещё одна линия a04 получила отмену в recovery и reconnect через pump.

Эта группа следует за прекращением клиентской сессии и не объясняет download спидтеста несколькими минутами раньше. Кнопку/причину остановки Android трасса не фиксирует; назвать её точно ручной остановкой нельзя. Новые тесты должны отличать прекращение peer-session от неожиданного carrier failure под нагрузкой.

### 2.3. Управляющий протокол занимает большую часть APPEND

| События сохранённой трассы | Клиент | Сервер |
|---|---:|---:|
| Завершённые observed IMAP operations | 26 651 | 33 647 |
| APPEND control | 11 474 | 10 717 |
| APPEND DATA, включая ошибки | 1039 | 1088 |
| `fetch_page` | 9378 | 11 507 |
| `reader_selection_audit` | 3734 | 6486 |
| `cleanup_fetch_exact` | 437 | 653 |
| `delete_observed_batch` | 436 | 1243 |
| `recovery_page` | 0 | 1735 |

Из 24 318 завершённых APPEND 22 191 относятся к control: **91.25% по количеству операций**. Это не доля байтов и не утверждение, что любой control бесполезен. `reader_selection_audit` иногда возвращает без сетевого SELECT; таблица содержит операции обёртки, а не точный счёт tagged-команд IMAP.

В исходящих control-записях: клиент STATUS 3469, GRANT_USAGE 1125, GRANT_RELEASE 1155, PING/PONG 1194; сервер STATUS 3751, PING/PONG 1272. В сумме STATUS — 7220 записей. Некоторые почти не меняющиеся idle-потоки имеют 140–167 STATUS за сохранённый интервал.

По разнице первых/последних health-снимков каждого аккаунта пустые FetchPolls составляют 55 283 / 64 287 = 85.99% на клиенте и 27 905 / 38 268 = 72.92% на сервере. Снимки аккаунтов сделаны в разное время, а FetchPolls не являются счётчиком wire-команд; эти числа описывают именно счётчики runtime. Сравнивать их с 9378/11507 `fetch_page` как одинаковые метрики нельзя.

Исходник подтверждает условия давления: hot-путь после пустого чтения спит 2 мс, активный — 10/20/40 мс; IDLE-аудит активной линии 250 мс; response-window PING 450 мс; периодические STATUS каждые 2 с и PING каждой линии каждые 5 с. Потоковая/линейная работа увеличивается вместе с количеством потоков и аккаунтов. Тест должен ограничивать агрегат команд на аккаунт и работу пустых линий, а не утверждать правильность конкретной паузы 2 мс.

### 2.4. Очереди и обслуживание существенно длиннее PING RTT

| Внутренний показатель | Клиент | Сервер |
|---|---:|---:|
| Максимум PumpQueueWait | 2713 мс, a02 | 7653 мс, a11 |
| Максимум ControlWait | 428 мс, a01 | 1518 мс, a02 |
| Максимум GrantSelectWait | 385 мс, a07 | 515 мс, a02 |
| Максимум stripe wait | 98 мс | 74 мс |
| Максимум observed APPEND DATA | 351 мс | 2761 мс |
| Максимум cleanup FETCH | 1326 мс | 2334 мс |
| ReaderHandoffs, последний снимок каждого аккаунта | 16 | 154 |

PumpQueueWait отражает время задания до допуска к writer; оно не доказывает, что весь интервал потрачен на один конкретный limiter. Максимум fast queue существенно меньше bulk: клиент 6 мс, сервер 95 мс. Поэтому bulk-передача может быть медленной даже при быстрых специальных PING/коротких обменах.

В серверных health-снимках общий governor ObservedRTT достигает лишь 496 мс; в клиентских — 519 мс. RateLimitMbps и EmergencyDecisionCount остаются 0; BulkLimit=5. Это не доказательство искусственного rate ceiling в данном запуске. Отдельное advisory download-window чаще около 1 МиБ, backlog доходил до 1 389 817 байт, pending до 720 896. Поскольку window и backlog меняются между snapshots и зависят от batch, эти максимумы нельзя вычитать друг из друга как одновременный снимок.

Прикладной RTT из спидтеста, специальный Echo по приоритетному control и обслуживание bulk — разные пути. Pooled RTT теряет плохие хвосты и не сообщает, сколько времени пользовательский DATA ждёт планировщика. Новая обратная связь должна отдельно учитывать delivery и residence-time каждого класса, не заменяя application probe быстрым PING.

### 2.5. Очистка читает значительный дополнительный объём

`gc_candidate_read.Bytes` — длина MIME, а не полезные байты приложения. На клиенте прочитано 16 784 461 байт MIME, на сервере 116 020 664. Сервер прочитал 246 крупных GC-кандидатов (>32 000 байт) и 11 552 маленьких. Все наблюдаемые locator `(account, UIDVALIDITY, UID)` в этой категории уникальны: циклическое повторное чтение одного UID данными не подтверждается.

Серверная очистка читает собственные исходящие DATA для доказательства физического объекта; приём DATA другой стороной — отдельная операция. Это существенная стоимость архитектуры доказательств, а не повод удалить доказательства. Сумма длительностей `cleanup_fetch_exact` — около 178 с на сервере и 75 с на клиенте, с параллелизмом между аккаунтами. Суммы не являются продолжительностью общей блокировки.

FIELD46 уменьшил число durable proof commits, но не убрал сетевое чтение для proof и конкуренцию cleanup с reader. `read_handoff.go` занимает cleaner для резервного FETCH уже после 200/350 мс отсутствия ingress progress; `RenewReaderSelection` использует тот же cleaner. На сервере 154 handoff против 16 на клиенте. Конкуренция подтверждена устройством кода, её вклад в скорость пока не выделен экспериментом.

Fsync выключать нельзя. Новая телеметрия должна раздельно измерять journal queue, commit, proof FETCH и DELETE/EXPUNGE. В новых полевых трассах нет такой декомпозиции, поэтому утверждение «всё тормозит из-за bbolt» было бы недоказанным.

### 2.6. TCP-выход VPS тоже даёт таймауты

Сервер отправил 59 OPEN_ERROR: 53 с ReasonTimeout=2, 6 с ReasonNetwork=7; все относятся к port 443. Сопоставление по пользователю и глобальному stream_id, независимо от crypto-session переносчика, даёт для всех 53 timeout OPEN→OPEN_ERROR 15 001–15 009 мс. Это соответствует 15-секундному deadline `TCPExit`: ожидание dial-slot, разрешение адреса и попытки соединения с конечным сайтом.

Возможны DNS/доступность target/очередь dials/последовательный обход адресов; конкретные hosts в трассе намеренно скрыты. Это не 53 ошибки LOGIN или IMAP APPEND. Для этого пути нужны события resolve, dial-admission, attempt по семейству адреса, target-connect-result с обезличенным target_id, отдельно от carrier errors. Это также важный источник фоновой нагрузки, которого нет у теста с единственным localhost echo-target.

ReasonTimeout RESET для двух серверных stream_id 55 и 333 отправлены по 61 раз, клиент для 619 — 55 раз через разные carriers. По коду это может включать повторное обнаружение expired state, не только сетевые повторы, поэтому назвать 61 отправку нарушением конкретного MaxSends без разбора состояния нельзя. Но terminal-state работа явно требует глобального owner и проверяемого конечного бюджета; смена helper-account не должна создавать новый бюджет. Нужен отдельный regression scenario: expired/half-closed stream, ACK потерян/задержан, carrier меняется, число повторов конечное.

## 3. Что говорят прежние результаты

Windows FIELD44: 10.635 Мбит/с / 576.764 мс; FIELD45 FIX1: 25.373117 / 566.6589. Это локальные четырёхлинейные тесты и не мобильные спидтесты. Их failure при пороге 27 не определяет причину низкого download Android.

Linux FIELD45 smoke: 38.532 / 362 мс; FIELD46: 36.637 / 333 мс. Разница около −4.9% по скорости и −8.0% по RTT в отдельных запусках. Это не paired A/B, и в FIELD45 mobile измерению мешала параллельная подготовка Gradle. На основании таких чисел нельзя объявлять небольшую регрессию или улучшение доказанным.

В отдельной 20-Мбит модели обе версии не прошли RTT: FIELD45 down/upload/duplex max 2875/2008/2225 мс, FIELD46 3078/2874/2508. FIELD46 useful averages down 11.136, up 11.520, duplex up/down 10.432/9.442 Мбит/с. Скорость модели выше пола 5, но задержка выше 1500; это устойчивый сигнал несоответствия требованию. Отсутствие локальных carrier exits не воспроизвело 15 реальных DATA socket failures из новых логов.

Мобильный стенд FIELD46 имеет важный пробел: при mobile=true он отключает эмпирический provider model, оставляет command-delay 1 мс и reliable shared relay с propagation RTT. Сеть и приложение моделируются, реальное распределение provider-command delays и socket-close behavior — нет. Старый field12 replay, когда включён, использует клиентские данные, размер MIME как признак APPEND-класса, отдельную искусственную длинную задержку на трёх аккаунтах. Это полезная проверка некоторых сценариев, но не полноценная модель текущего FIELD46.

Тест `TestFieldHTTPSColdStartAndDuplex130` считает серию последовательных probes; когда ответ длиннее секунды, измеряемая фаза удлиняется. В FIELD46 nominal 30 с превратились в 33.497, 33.313 и 45.283 с. Итоговая скорость использует фактическое время, что правильно, но это уже разные окна нагрузки. Нужен независимый фиксированный measurement-clock: поздняя проба не переносит конец окна, хвост считается по времени, а timeout остаётся в результатах.

## 4. Сравнение схожих проектов по коду

Исходники получены через GitHub; commits закреплены 7 октября 2026. Никакие их бинарники и установочные скрипты не запускались. Заявленные авторами Mbps не перепроверены.

| Проект / commit | Фактическая модель | Полезный механизм | Что не переносить как доказанный результат |
|---|---|---|---|
| Maxu46/imap-tunnel-proxy / `741f6748a5d016e5df0c8af83091ec1bcad9d955` | IMAP-подобное рукопожатие со своим сервером; затем `BINARY`, ответ `299`, raw binary frames по тому же socket | Простой multiplex и ограничение канальной работы | Это не APPEND/FETCH транспорт через внешний mailbox. Высокая скорость прямого TCP ничего не доказывает для очереди писем |
| noiseonwires/true-imap-tunnel / `057677b05679be80bdba0c20cfa34b6336224a97` | Sender + Watcher на аккаунт; APPEND batches, IDLE/poll, UID cursor, по умолчанию stream affinity | Opportunistic batching; backpressure; UIDVALIDITY и cursor на reconnect; bounded DONE wait; deferred expunge; opt-in реальный HTTP test | In-memory путь и повтор APPEND после неопределённой ошибки не заменяют durable physical accounting ITOM; заявленная стабильность провайдеров анекдотическая |
| Nikkkaaws/White-Shield-IMAP-Transport / `eeed0cf9c34619008e2aa3ee8c70bfedac98fd1b` | Send/receive lanes, account budget, отдельные APPEND workers, reorder + ACK/NACK + bounded recovery | Общий account cooldown; крупные bulk batches; короткие priority batches; fixed residence deadline; coalescing TCP read; scheduling по drain/delivery time | Его RTT и benchmarks относятся к другим endpoints, accounts и physical links; сохранность quota через crash не установлена этими кодовыми путями |

### 4.1. Maxu46: отличающийся транспорт

В `client.py::_imap_handshake` после AUTH отправляется нестандартная строка `BINARY`; `_receiver_loop` читает binary frame header, `send_frame` пишет его прямо в TCP. `server.py::_imap_handshake` принимает этот marker и `_binary_mode` обслуживает raw frames. Поэтому проект не является подходящим образцом реализации почтовой очереди. Раздел `common.py` содержит генераторы/прочие IMAP-related helpers, но определять рабочий datapath следует по вызовам client/server, а не наличию этих классов.

Источники: [client.py](https://github.com/Maxu46/imap-tunnel-proxy/blob/741f6748a5d016e5df0c8af83091ec1bcad9d955/client.py), [server.py](https://github.com/Maxu46/imap-tunnel-proxy/blob/741f6748a5d016e5df0c8af83091ec1bcad9d955/server.py). Метаданные лицензии: GPL-3.0; код не включён в предлагаемый ITOM-патч.

### 4.2. True IMAP Tunnel: упрощение жизненного цикла

`Sender` владеет APPEND connection и очередью. Пока один APPEND выполняется, накапливается следующая пачка; drain использует её без дополнительного batching delay при уже готовой работе. `Watcher` хранит cursor отдельно от срока жизни connection, сохраняет его при прежнем UIDVALIDITY, проверяет новый generation, завершает IDLE с ограниченным ожиданием tagged DONE. Удаление обрабатываемых писем и expunge разделены.

Multipath по умолчанию сохраняет account affinity потока; frame-round-robin экспериментален. ITOM сейчас содержит более сложное разделение owner/carrier/grants. Рекомендуется сначала сравнить throughput устойчивой chunk-affinity и нескольких параллельных TCP-потоков, затем включать striping, если доказана польза. Affinity не должна делать целый поток невосстановимым при отказе аккаунта.

`appendWithRetryLocked` повторяет APPEND после ошибки с новым connection; для ITOM такую схему нельзя копировать буквально: accepted-with-lost-tagged-response создаёт настоящий дополнительный объект и долг. Retry должен проходить через тот же attempt ledger и ограничение физических попыток.

В `TestRealIMAPHTTP` есть отдельный opt-in путь: реальные LOGIN/папки, HTTP-потоки и hash; отсутствие credentials приводит к SKIP. Такой тест полезен как модель организации реального gate, но не гарантирует 5/1500 и не заменяет Android TUN испытание.

Источники: [sender.go](https://github.com/noiseonwires/true-imap-tunnel/blob/057677b05679be80bdba0c20cfa34b6336224a97/internal/imap/sender.go), [watcher.go](https://github.com/noiseonwires/true-imap-tunnel/blob/057677b05679be80bdba0c20cfa34b6336224a97/internal/imap/watcher.go), [multipath.go](https://github.com/noiseonwires/true-imap-tunnel/blob/057677b05679be80bdba0c20cfa34b6336224a97/internal/tunnel/multipath.go), [real_imap_test.go](https://github.com/noiseonwires/true-imap-tunnel/blob/057677b05679be80bdba0c20cfa34b6336224a97/internal/tunnel/real_imap_test.go). Лицензия по metadata: LGPL-3.0.

### 4.3. White Shield: account budgets и coalescing важнее числа workers

`accountBudget` ограничивает параллельные APPEND всех folder lanes одного аккаунта и вводит bounded cooldown/quota signal. `sendLoop` собирает bulk до provider-sized bounds, а timer запускает на первом элементе: непрерывный трафик не перезапускает максимальное время пребывания пачки. Маленькая priority DATA получает короткое отдельное окно объединения; первые байты потока не становятся бесконечно привилегированными.

`readStreamChunk` использует sliding idle 10 мс, target 64 КиБ и wall-clock cap 125 мс. ITOM coalescing до 2 мс с абсолютным deadline чаще превращает WAN-фрагменты в отдельные работы. Это гипотеза, которую проверяют на разном spacing TCP-записей; нельзя сразу назначить всем потокам 125-мс задержку.

В `EXPERIMENTS.md` автор явно описывает завышенные результаты ранних compressible fixtures, затем incompressible payload. Для Rambler приводятся локальные 144–189 Мбит/с, но public HTTPS download лишь 21.06 Мбит/с, fresh HTTPS 2.305–2.422 с, warm 1.027–1.042 с. Это поддерживает необходимость разделить local transfer, remote Internet и HTTP latency; цифры не становятся обещанием ITOM на мобильной сети. Важно также, что для Rambler проект выбирает polling, а не IDLE. Делать IDLE единственной стратегией нового ITOM нельзя.

Источники: [engine.go](https://github.com/Nikkkaaws/White-Shield-IMAP-Transport/blob/eeed0cf9c34619008e2aa3ee8c70bfedac98fd1b/internal/engine/engine.go), [client.go](https://github.com/Nikkkaaws/White-Shield-IMAP-Transport/blob/eeed0cf9c34619008e2aa3ee8c70bfedac98fd1b/internal/imapc/client.go), [engine_test.go](https://github.com/Nikkkaaws/White-Shield-IMAP-Transport/blob/eeed0cf9c34619008e2aa3ee8c70bfedac98fd1b/internal/engine/engine_test.go), [EXPERIMENTS.md](https://github.com/Nikkkaaws/White-Shield-IMAP-Transport/blob/eeed0cf9c34619008e2aa3ee8c70bfedac98fd1b/EXPERIMENTS.md). Лицензия по metadata: MIT.

## 5. Архитектура нового ядра

### 5.1. Владельцы состояния

| Компонент | Единственная ответственность | Что ему запрещено делать |
|---|---|---|
| LogicalSession / StreamEngine | Глобальные stream IDs, offsets, receive window, half-close, terminal state, retry budget | Хранить независимую копию одного stream на каждом carrier; выполнять сетевой IO под своим lock |
| AccountScheduler | Общие physical connection/APPEND/command budgets, справедливые очереди двух пользователей, cooldown, выбор работ | Выдавать допуск до проверки актуального socket generation; сбрасывать protocol state при замене socket |
| AppendWorker | Один живой socket, сериализованные команды, fixed batch residence-time, typed completion | Самостоятельно повторять неизвестный APPEND, возвращать долг по EOF, скрывать first-close-cause |
| ReceiveWorker | Один command stream, UID cursor + gaps, bounded streaming, IDLE/DONE или adaptive poll | Одновременно использовать cleaner как резервный reader; считать partial/untagged ответ полным proof |
| Proof/QuotaService | Durable grants/attempts, proof cache, batched observe/delete/absence commits | Приравнивать receipt к удалению, отключать fsync, читать сеть под lock session/stream |
| Metrics / AcceptanceRunner | Useful delivered bytes, queue-age, application RTT, separate target/carrier failures | Суммировать направления как directional Mbps; исключать late probes/timeouts для получения PASS |

AccountScheduler создаётся на аккаунт, а не на user/lane; все пользователи и namespace этого аккаунта используют общие бюджеты. Сохраняются отдельные ключи, папки/namespace и durable scopes пользователей. Число соединений зависит от реально измеренного provider cap; допускаются reduced-capacity профили. Выдать каждой новой папке по несколько соединений — не стратегия ускорения.

### 5.2. Очереди и batching

Использовать три явных класса: urgent control/короткий bounded prefix DATA, bulk DATA, maintenance. Каждый имеет конечный byte budget, age limit и справедливость по пользователям. Control aggregation обновляет cumulative ACK/window/status, не допускает дублирования одного state-update из разных владельцев. Для неактивного потока periodic STATUS не генерируется без outstanding delivery/flow-control/close работы; liveness учитывается отдельно.

Batch collector имеет фиксированное максимальное время с момента первого элемента. Под нагрузкой сразу забирает уже готовую пачку; редкие маленькие записи объединяет коротким окном. TCP coalescing имеет одновременно idle deadline и wall-clock cap. Стартовые размеры, например 128–512 КиБ bulk и короткое окно 2–10 мс для small DATA, — параметры эксперимента, не новые жёсткие критерии. Megabyte batch из другого проекта нельзя автоматически переносить на 20-Мбит доступ: 1 МиБ MIME занимает около 419 мс канала уже без RTT и прочих затрат.

Назначать stream chunk одному writer/account на ограниченный квант; распределять параллельные потоки по аккаунтам. Затем сравнить с bounded striping по квантам. При потерях/visibility gap меняется carrier, а owner stream и retry ledger остаются одними. Управляющие FIN/RESET содержат ordered terminal boundary и не могут обогнать недостающие байты без зафиксированного правила прекращения потока.

### 5.3. Receive и control

Нужны оба режима: event-driven IDLE с безопасным DONE и bounded polling для провайдеров, где visibility/IDLE хуже. Автовыбор основан на измерениях конкретного аккаунта, без запрета неизвестных IMAP-провайдеров.

Убрать speculative reader handoff на cleaner через 200/350 мс как основной механизм latency. У read command есть progress-aware deadline; maintenance scheduler не конкурирует с ним за один client незаметно. Резервный receive worker допустим только как явно ограниченный ресурс account scheduler с одним flight/cursor и проверяемым join.

Отдельные control mailbox/reader могут убрать BODY head-of-line от крупных DATA, но требуют нового folder layout, connection budget и negotiation. Это отдельный эксперимент после baseline: по умолчанию нельзя добавлять сокеты поверх уже занятого provider cap. В низком лимите сохраняется один reader с bounded BODY pages и отдельными очередями; физическую изоляцию нельзя обещать там, где её нет.

### 5.4. Socket lifecycle и ошибки

Ввести состояния connecting→ready→busy/idle→draining→dead→cooldown и generation. First-close-cause сохраняется один раз; последующие `net.ErrClosed` ссылаются на неё. Логировать socket_id, role, account, generation, operation stage, idle_age, last_io_age, tagged status category, local-cancel cause, elapsed и bytes-written category. LOGIN password, literal bytes и target hostname в trace не пишутся.

Отдельно обрабатывать: отказ до начала APPEND; явный tagged NO/BAD; неопределённый исход после начала публикации; dead idle worker; provider throttle; connection cap; deliberate session stop. Старый живой stream не получает reset только потому, что reader/writer socket заменён. Потеря tagged ACK после записи не запускает произвольный автоматический resend.

Простой DATA worker либо проверяется bounded keepalive, либо закрывается намеренно после известного idle interval и открывается перед следующей работой. Выбор зависит от provider behavior и цены LOGIN. Ждать ошибки следующего большого APPEND после уже замеченного decoder EOF нельзя. Сервер и Android применяют одинаковую семантику lifecycle; лимиты провайдера сохраняются на общих аккаунтах.

### 5.5. Backpressure и proof

Оставить один явный delivery-flight budget и отдельный authoritative physical quota. Полезная скорость, mail bytes, pending APPEND, received bytes и application-written bytes — разные счётчики. Не выводить новую «пропускную способность» из низкого rate после простоя и не уменьшать на ней же окно, которое мешает получить следующую пробу. RTT PING используется как liveness/контроль, а bulk queue-age и end-to-end application probes — как оценка загрузки.

До публикации проверить все admission условия, не удерживая сетевой slot при ожидании rate/window/quota. Durable Start обязательно предшествует физическому APPEND, а unknown outcome сохраняет долг. Порядок admission должен быть описан одним контрактом; сейчас часть плановых ограничений вызывается после подготовки grant/attempt, что усложняет отмену и раздувает состояния.

Proof service переиспользует уже реально прочитанные проверенные Message observations через bounded cache `(account, folder, UIDVALIDITY, UID, size, digest)`. Локальный отправленный body и APPENDUID сами по себе не подменяют наблюдение provider-body при действующем proof-контракте. Неизвестные/старые объекты проходят отдельное reconciliation чтение. Очистка объединяет commits, имеет byte/command budget и резерв своевременного освобождения квоты, поэтому не может откладываться бесконечно ради скорости.

Устранение обязательного S2C read-back для новых accepted objects возможно только отдельным новым proof-контрактом и доказательством safety. Это не входит в первый baseline и не достигается доверием peer receipt. Никакие refund/delete assertions старого journal suite не ослабляются ради Mbps.

### 5.6. Версии и переключение

Сначала заменить внутренние роли за нынешним mailbox/wire интерфейсом и сравнить на той же нагрузке. Новый mailbox layout или изменённые ACK/proof DTO требуют явной версии протокола и совместного обновления APK/VPS. Нужны feature negotiation, отказ от несовместимого peer без потери старого ledger, отдельное имя кандидатской ветки и rollback к сохранённой исходной поставке. Не переносить старые физические locator между UIDVALIDITY и не увеличивать квоты при миграции.

## 6. Новый Go test contract

### 6.1. Три разных результата

1. **Корректность:** unit/property/fault/lifecycle/race, exact bytes, bounded ресурсы, cancellation и durable recovery. Быстро и воспроизводимо; большинство state machine сценариев используют virtual clock и барьеры, а не sleep.
2. **Производительность модели:** calibrated local IMAP peer, реальные protocol/socket этапы, explicit shared 20-Мбит shaping, фиксированный measurement clock. Это отдельный отчёт параметров и результатов.
3. **Полевая квалификация:** настоящие Android TUN, VPS, конкретные почтовые аккаунты, direct baseline и идентичные candidate/baseline серии. Отсутствие этого запуска даёт NOT_RUN, а не PASS.

Старый 27-Мбит localhost smoke перестаёт быть универсальным доказательством или произвольным CPU-зависимым препятствием сборке. Его integrity/lifecycle assertions остаются tests; скорость уходит в benchmark/модельный gate с явно указанным профилем. Это изменение организации тестов, не снижение 5/1500. Перед стабильным релизом профиль 20 должен проходить свой точный gate.

### 6.2. Минимальные обязательные сценарии

| Новый сценарий | Основание | Проверяемое поведение |
|---|---|---|
| Idle writer EOF before reuse | 15 immediate Unknown ошибок | Worker становится dead по первому отказу; новая работа не уходит в этот generation; другие роли/streams живы |
| Accepted APPEND, lost tagged reply | Неопределённый APPEND | Долг остаётся; повтор учтён и ограничен; после recovery приложение получает bytes ровно один раз |
| One account throttle / cap / BYE | Несколько workers и users | Account cooldown без dial storm, конечные очереди, справедливость, сохранение healthy accounts |
| Tagged DONE stall / delayed UID visibility | Reader handoffs и пустые FETCH | Нет второго неуправляемого flight/cursor; UID gaps не пропускаются; нет fetch storm |
| Bulk + cleanup + 30–60 background streams | STATUS/GC/очереди в новых трассах | Control короткий, bulk прогрессирует, command budget общий, maintenance не монополизирует account |
| WAN fragmented reads with 1/5/10/25 ms spacing | 2-ms coalescer против WAN | Нет маленького APPEND на каждый фрагмент; bounded latency короткого потока; fixed max batch age |
| Expired/half-closed stream across carriers | RESET 61/61/55 повторов | Один terminal owner, конечный общий retry budget, late ACK корректен, после retirement нет STATUS/RESET churn |
| Target TCP 443 stall at 15s | 53 OPEN_ERROR timeout | Target error виден отдельно; dial admission bounded; соседние healthy transfers не остановлены |
| Session stop / peer silence | Reconnect после client stop | Deliberate stop не записан как spontaneous outage during measurement |
| UIDVALIDITY change / holes / out-of-order visibility | IMAP validity и cursor контракт | Ни skipped body, ни чужого delete/refund; восстановление живого logical stream |
| Slow/failed fsync, lost commit acknowledgement | Durable quota safety | Откат/poison/reopen сохраняют физический долг; actor не держит network IO под journal lock |
| Same account, two users | Принятый deployment ITOM | Общие caps, отдельные keys/scopes/quotas, fairness и отсутствие перекрёстной выдачи payload |
| Warm/cold HTTP/HTTPS, SOCKS/TUN, DNS | Echo не равен браузеру | Измеряются connect/TLS/TTFB/warm request отдельно; exact bytes и target failures включены |
| Fixed-window acceptance with delayed probes | Фаза 30→45 секунд | Late probe не переносит окно; timeout учитывается; tail зависит от времени, а не номера probe |

Модель провайдера — stateful IMAP peer: limits LOGIN/APPEND по аккаунту, распределение service time по сторонам, общая нагрузка control/DATA/GC, видимость UID между сессиями, tagged/untagged ordering, IDLE DONE, EOF/reset, COPY/DELETE/EXPUNGE варианты. Простого `Sleep(80ms)` перед каждым handler недостаточно. Входные длительности из этого комплекта содержат round-trip и клиентское ожидание, поэтому их нельзя целиком прибавлять к отдельному propagation RTT: иначе сетевой latency учитывается дважды. Калибровать модель по baseline и выделять service/queue/path компоненты.

State tests с virtual clock не подтверждают Mbps. Сетевой integration/performance стенд использует реальное время, bound на in-flight memory и actual delivered bytes. Payload incompressible, уникальный для session/user/direction/stream, с hash/length проверкой; compressed repetitive payload запрещён для приёмки скорости. Равномерная искусственная задержка не заменяет correlated spikes при одном account.

### 6.3. Точный профиль 20 Мбит/с

- Общий физический client access bottleneck 20 Мбит/с, нулевые намеренные потери; все client↔IMAP sockets, TLS/MIME/control/cleanup bytes, использующие этот доступ, проходят через него. Server-side access и отдельные provider limits задаются независимо.
- Сначала калибровка shaper без ITOM и базовая задержка; тест не может тихо раздать по 20 Мбит/с каждому connection. Режим shared-duplex и full-duplex uplink/downlink задаётся явно; кандидаты сравниваются в одном режиме.
- Warmup 10 с, measurement 60 с, last-third 20 с; фазы download, upload, duplex. Четыре bulk потока на каждое активное направление; отдельный сценарий background browser streams и отдельный single-stream.
- Полезная скорость **>=5.000 Мбит/с** в каждом активном направлении по всему measurement и последним 20 с. Up+down не суммируются для прохождения одного направления.
- Максимальный **прикладной warm RTT <=1500 мс**, включая поздние ответы; timeout — FAIL. Границы включительные, без 10% допуска. Cold connect/TLS/TTFB сохраняются отдельными числами; нельзя выдать их 5-секундный smoke за выполнение 1500-мс RTT.
- Probe schedule/byte-clock независимы. Отправленная до конца окна проба учитывается даже если завершилась позже; её elapsed не расширяет окно bandwidth. Очередь измерителя ограничена; missed scheduling сохраняется как ошибка стенда/измерения.
- Нет повреждения/недостачи bytes, неожиданной потери logical stream или здоровой линии в zero-fault steady-state. В fault-scenario ожидаемые disconnect выделены заранее, recovery не прячет downtime из знаменателя.
- Healthy baseline/candidate не менее трёх парных повторов в одинаковых условиях и чередующемся порядке; все повторы и неудачи остаются в отчёте. Сетевые параметры, payload, accounts/users и endpoints неизменны.

Успешный внешний спидтест на неизвестной физической сети сам по себе не квалифицирует 20-Мбит профиль. Он остаётся полезным полевым симптомом и должен связываться с точным interval/measurement_id и trace обоих концов.

## 7. Порядок реализации и выпусков

| Этап | Конкретный результат | Точечная проверка в разработке |
|---|---|---|
| P0 — evidence и observability | socket first-close-cause/generation, target dial stages, stage-wise wait/commit/GC, глобальный terminal retry trace; redacted FIELD46 fixtures | Ошибка idle socket, cancel-vs-peer EOF, target timeout, trace privacy; воспроизводится хотя бы один наблюдавшийся отказ |
| P1 — простой baseline | Один AccountScheduler с bounded worker pool и двумя пользователями; stream state отдельно от carrier; current wire adapter | Socket replacement без stream reset; fairness; no unknown retry/refund; queue bounds |
| P2 — полезные пачки и receive | Fixed residence batching, ограниченный TCP coalescing, provider mode IDLE/poll, без неявного cleaner handoff | Fragmented WAN writes; command budget; DONE stall; UID gaps; concurrency с cleanup |
| P3 — control и proof scheduling | Cumulative/coalesced control, idle state silence, global terminal budget, bounded proof cache/commits, GC fairness | Stop/reset/half-close; stalled cleanup; retained observation reuse; fail-before/after commit |
| P4 — общая delivery обратная связь | Однозначный flight budget, измеримые admission stages, application probes независимо от PING | 20-Мбит download/upload/duplex; small+bulk; никаких двойных ограничений/скрытых refill |
| P5 — полевой сравнительный кандидат | APK и сервер с одинаковой revision, reproduction bundle, A/B с сохранённой поставкой | Реальный Rambler/Android/VPS и direct baseline; исходные trace+results |

Промежуточные кандидаты выпускаются небольшими проверяемыми изменениями. В разработке — только соответствующие точечные tests. Перед упаковкой каждого runtime-релиза — один общий прогон всех обязательных проверок на точных исходниках; benchmark/model/field результаты публикуются отдельно с PASS/FAIL/NOT_RUN. Экспериментальный кандидат с performance FAIL обозначается явно и не называется стабильным. Полная квалификация требует 5/1500 и отсутствия неоговорённых сбоев; успешный compilation/local suite её не заменяет.

Первым кодовым изменением должен стать P0 и воспроизводимый idle-worker failure scenario, затем P1. Начинать с увеличения workers, batch size, physical quotas или ослабления RTT/Fsync было бы преждевременно. Это порядок решения выявленных зависимостей, а не просьба пользователя снова вручную подтвердить уже порученную разработку.

## 8. Комплект и воспроизводимость

В комплекте:

- этот отчёт;
- `analysis-summary.json`: счётчики, длительности, ошибки и health по сторонам; межмашинные разности помечены NOT_RTT;
- `field46-replay-inputs.json`: раздельные по стороне duration samples и error events как входные данные будущей модели; это ещё не выполняемые Go-тесты;
- `reference-provenance.json`: commits и фактически прочитанные файлы проектов;
- `analyze.py`: воспроизводимый локальный подсчёт из распакованных trace;
- `README.txt` и `SHA256SUMS.txt`.

Новые диагностические архивы не перепубликуются в комплекте; raw payload/credentials/target hosts в агрегаты не добавлялись. Полные исходники исследованных сторонних проектов не вендорятся. Для воспроизведения распаковать клиентский архив в `diagnostics/client`, серверный в `diagnostics/server` рядом с analyze.py и выполнить `python analyze.py`. Исторические результаты воспроизводятся по исходным reports соответствующих версий, не новым общим запуском tests.

Точечная проверка этого этапа: целостность `seq`, согласование counts с JSON, отсутствие credential/host полей в экспортированных агрегатах, привязка обеих версий field25, корректная маркировка несинхронизированных часов. Runtime не изменялся; общий Go/Android прогон в этом аналитическом этапе не выполнялся.
