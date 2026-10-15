> Core4 amendment: WRITE_GRANT / GRANT_USAGE / GRANT_RELEASE / GC_STATUS /
> GC_PROOF_REQUEST now have typed storage contracts in `REMOTE_GRANTS_V1.md`.
> REKEY remains unsupported; the full distributed runtime is not integrated.

# ITOM wire 2: initial control contract v1

Source revision: core-2-control-1. Дополнение к TRANSPORT_CONCEPT.md,
не новая модель транспорта. Это первая точная кодировка базовых control типов;
в core-1 были только зарезервированы их числовые идентификаторы.

## Формат

Envelope и внутренний frame header остаются прежними. Все целые числа unsigned,
big-endian; offsets и абсолютные окна не превышают 2^63−1. `logical_id=0`
для всех control. Неиспользуемый offset равен нулю. Payload должен иметь точную
длину: trailing bytes, усечение и неизвестные значения отвергаются. Приём:
Noise authentication → ParseFrame → DecodeControl → проверка состояния/роли.
Одна проверка AEAD не заменяет семантическую проверку сообщения.

Stream ID — положительный нечётный идентификатор. Session-scoped сообщения имеют
stream=0. Максимальный payload 4096 bytes; новый максимальный NACK — 521 bytes.

| Типы | Scope | Offset | Payload |
|---|---|---|---|
| CONFIRM/READY/READY_ACK (1/2/3) | Session | 0 | Empty |
| OPEN (16) | Stream | 0 | port:u16, host_length:u16, host:ASCII[host_length] |
| OPEN_OK (17) | Stream | 0 | Empty |
| OPEN_ERROR (18) | Stream | 0 | reason:u16, ненулевая причина |
| RECEIPT (33) | Stream | contiguous receipt | Empty |
| WINDOW (34) | Stream | absolute MAX_STREAM_DATA | absolute MAX_DATA:u64 |
| STATUS (35) | Stream | attempted end | receipt:u64, max_stream:u64, max_data:u64, final:u64, has_final:u8 |
| STATUS_REQUEST (36) | Stream | 0 | Empty |
| NACK (37) | Stream | 0 | request_id:u64, count:u8, count × (start:u64,end:u64) |
| FIN/FIN_ACK (48/49) | Stream | final offset | Empty |
| RESET/RESET_ACK (50/51) | Stream | final accounting offset | reason:u16, ненулевая причина |
| PING/PONG (64/65) | Session | 0 | token:u64, ненулевой |
| SESSION_CLOSE/SESSION_ERROR (66/67) | Session | 0 | reason:u16; ERROR требует ненулевую причину |

Reason: 0=None, 1=Refused, 2=Timeout, 3=Policy, 4=Capacity, 5=Protocol,
6=Closed, 7=Network. Никакие свободные тексты с credentials/адресами/ошибками ОС
не передаются как причина. Преобразование системных ошибок делает будущий worker.

OPEN host — canonical IP literal без brackets/zone/IPv4-mapped IPv6 либо
lowercase ASCII DNS name (в том числе IDNA A-labels). Длина 1…253, label 1…63,
без empty labels, leading/trailing hyphen, trailing dot, URI, whitespace и
wildcards. Неоднозначные числовые формы вроде 127.0.0.01 отвергаются. Unicode
нужно нормализовать до вызова codec. Port 1…65535. Codec не выполняет DNS и
не определяет серверную destination policy — это обязанности server dial layer.

STATUS: attempted/final относятся к потоку bytes отправителя STATUS. Receipt и
окна относятся к его приёмнику (обратному направлению). Поэтому attempted и
receipt напрямую сравнивать нельзя. Receipt <= max_stream; has_final строго
0/1; при 0 final=0; при 1 final>=attempted. Final может включать ещё не
отправленные bytes. Attempted high-watermark сохраняется после RECEIPT и не
включает bytes, которые оставались только в очереди.

NACK содержит 1…32 отсортированных непустых half-open ranges [start,end).
Пересекающиеся/соседние диапазоны запрещены: соседние объединяются до кодирования.
Request ID ненулевой; scope — session/direction/stream. Receiver не переиспользует
ID для нового запроса. Sender связывает диапазоны с собственными immutable DATA
items и применяет existing per-item repair budget: не больше трёх попыток всего.
DecodeControl само по себе не разрешает APPEND и не расходует WRITE_GRANT.

WINDOW stream-scoped и несёт одновременно stream/connection absolute limit.
Оба значения обновляются независимо монотонно, invalid snapshot не обновляет
ни одного. Начальное разрешение выдаётся WINDOW для первого открытого stream;
до этого sender credit=0. Connection-only WINDOW со stream=0 не поддерживается
этим контрактом. Ни WINDOW, ни RECEIPT не освобождают physical mailbox debt.

Reserved REKEY_*, WRITE_GRANT/GRANT_USAGE/GRANT_RELEASE, GC_* пока возвращают
CONTROL_UNSUPPORTED. Их точные схемы появятся вместе с rekey coordinator и
журналом, чтобы wire semantics соответствовала реальному владению ресурсом.
Полный milestone 3.0 пока не завершён.

## Активация начальной сессии

`session.Gate` создаётся только после успешного Noise split. Один владелец
сериализует вызовы; входные записи уже проверены `secure.Epoch.Open` на binding.
Время — elapsed monotonic duration, не Unix timestamp. Generation ненулевое,
выдаётся внешним runtime; старые callback после Stop отклоняются.

Client: AwaitReady → Established после READY. Первое действие — CONFIRM,
ответ — READY_ACK. Server: AwaitConfirm → AwaitAck после CONFIRM → Established
после READY_ACK. OPEN запрещён до этого, кроме точного исключения: валидный,
аутентифицированный OPEN в AwaitAck служит доказательством получения клиентом
READY, если READY_ACK потерян. В AwaitConfirm такой OPEN отвергается. DATA
неизвестного stream не может обойти барьер начальной сессии.

Duplicate CONFIRM/READY даёт актуальный READY/READY_ACK с ограничением частоты.
READY_ACK ничего не подтверждает в ответ. Poll повторяет только ожидаемые
CONFIRM или READY. Default: deadline=45s, interval=2s, max=16 sends на шаг.
Исчерпание send budget означает ожидание deadline, а не немедленный RESET.
Duplicate-triggered ответы расходуют тот же budget. Established client отвечает
на поздний READY в пределах startup deadline, затем не поддерживает вечный
ACK traffic. Успешная сессия при истечении startup deadline не закрывается.

Каждая повторная logical control запись шифруется заново с НОВЫМ Noise counter.
Повтор старого ciphertext будет правильно отброшен replay-window. Это отличается
от Noise handshake messages, которые повторяются теми же cached bytes.

GateAction.Activated возникает однократно, Deliver разрешает stream handling,
Send содержит не более одного frame. Никакие goroutines, сокеты, таймеры,
семантика network reconnect или замена ключей этим типом не создаются. Для
rekey нужен отдельный контроллер: текущий Gate нельзя объявлять готовым rekey.

## Идемпотентный OPEN и владение результатом dial

Клиентский OpenRequest создаётся после Gate.Ready(), с новым stream ID. Он
повторяет неизменённый OPEN, принимает один OPEN_OK/ERROR, хранит результат,
ограничивает retries/deadline и не разрешает DATA до OPEN_OK. Late success после
deadline/Stop не возвращает stream в работу. Внешний runtime после timeout
должен завершить terminal accounting/RESET; этот класс не выполняет их сам.

Серверный OpenBook получает OPEN только после Gate разрешения. Он выдаёт ровно
один DialJob на identity (generation, stream). Пока dial выполняется, duplicate
не создаёт новый job. Изменение host/port для существующей identity — ошибка.
После completion duplicate получает cached outcome с cooldown. Ошибочный
исход не вызывает второй dial. Dial deadline завершается Cancel token и
OPEN_ERROR(Timeout), в том числе если late completion пришёл до PollOne.

Worker владеет созданным сокетом до принятия completion. `Adopt=true` возникает
ровно один раз при успешном своевременном результате. Если Adopt=false (даже
при ошибке метода, Stop или старом generation), worker ОБЯЗАН закрыть результат.
Реальные dial workers ещё не добавлены; unit/integration tests проверяют решение
о владении, а не открывают интернет-сокеты.

Все admitted IDs резервируют future tombstone. Retire удаляет cached outcome
после terminal accounting, оставляя tombstone навсегда в этой сессии. Поздний
OPEN закрытого ID получает Closed и никогда не выдаёт DialJob. При исчерпании
metadata/Open capacity внешний runtime должен явно завершить сессию; нельзя
отправить terminal OPEN_ERROR для незапомненного ID и позднее его принять.
Failed/pending outcomes также входят в maxActive до Retire. Retire pending work
требует отмены worker внешним владельцем. Stop требует cancellation context и
закрытия всех уже adopted sockets внешним runtime.

Это детерминированные компоненты actor, а не завершённый event loop/daemon.
Глобальный control budget, terminal FIN/RESET controllers, cleanup, хранение
состояния и physical reservations остаются следующими обязательными частями.

## Обнаружение пропусков

Tx.AttemptedEnd помнит верхнюю границу DATA, переданной writer (включая unknown
commit). Rx.Missing сравнивает её с реальными received intervals и continuous
receipt. Поэтому первый/последний/единственный потерянный DATA обнаруживается
через STATUS без следующего DATA. Ответ bounded: максимум 32 earliest gaps.
Запоздалый STATUS ниже receipt безвреден. STATUS за пределами выданного stream
окна или final отвергается. Поиск gaps не меняет окна, receipt или physical debt.

Периодический STATUS scheduler и применение NACK к scheduler всего runtime ещё
предстоят. В существующем fault integration suite STATUS/NACK теперь проходят
через настоящий Noise и codec; gap вычисляет Rx, его не сообщает harness.

## Проверки

21 независимый binary fixture в wire/testdata/control2.json подготовлен через
Python struct (big-endian layout), а не через проверяемый Go encoder.
Новая ревизия содержит 51 top-level Go Test + 3 Fuzz targets (было 32 + 2).
19 новых Test покрывают границы parser, recovery, startup, single-dial, stop,
late completion, credit snapshots. Subtests и fuzz seeds считаются отдельно
в отчёте Go, число test functions не равно passed_test_events.

Сценарии Noise интеграции: READY_ACK loss, OPEN_OK loss, duplicate OPEN без
повторного dial/adoption; отдельно authenticated malformed control. Старый
16-seed DATA fault suite теперь использует actual STATUS-derived NACK.

Go-исполнение этой ревизии делается локально пользователем через target core.
Сохранённый reports/core1-windows-20260925/core-report.json относится только
к core-1-fix-1, не доказывает прохождение новых тестов. APK/server не собирались.
