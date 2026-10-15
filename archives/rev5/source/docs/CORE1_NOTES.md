# core-1: реализация и открытые проверки

Дата: 2026-09-24. База `ui-final-1`, SHA-256 архива:
`94873026f29cdcc3e5f1269ea42a06af8674238c80f251908002b40fb9b75a1d`.

Это библиотечная основа, не mail/VPN runtime. Go здесь не запускался;
наличие тестов не означает их прохождения. Gates концепта остаются обязательными.

## Wire и Noise

Все целые big-endian. Envelope 64 байта:

| Offset | Bytes | Поле |
|---|---:|---|
| 0 | 4 | ASCII ITM2 |
| 4 | 1 | version=2 |
| 5 | 1 | handshake=0, record=1 |
| 6 | 2 | нули |
| 8 | 16 | pair ID |
| 24 | 16 | session ID |
| 40 | 4 | ненулевой epoch |
| 44 | 1 | C2S=0, S2C=1 |
| 45 | 3 | нули |
| 48 | 8 | Noise counter; для handshake индекс 0/1 |
| 56 | 4 | body length |
| 60 | 4 | нули |

Inner header: type(1), reserved(3 нуля), stream(8), logical(8), offset(8),
payload length(4). DATA type=32, stream нечётный ненулевой, logical ненулевой,
offset и stream ≤2^63−1, payload 1…32 768. Остальные type codes зарезервированы
в `wire.go`. **Полные control payload schemas ещё не реализованы**: codec
проверяет общую форму и лимит 4 KiB. До будущей типизированной проверки
управляющие сообщения нельзя передавать runtime-обработчику.

Noise plaintext = копия envelope + inner header + payload. Стандартный
CipherState получает nil AD; после decrypt копия envelope сравнивается с внешним
заголовком. Max record=32 944. Нет извлечения Noise-ключей или собственного AEAD/KDF.
Prologue: фиксированный protocol label, Server ID, User ID (8 цифр), Pair ID,
Session ID, epoch, Handshake ID, ConfigHash; размеры/порядок в Binding.prologue.
Handshake payload пустой. Каноническое вычисление ConfigHash/bootstrap ещё предстоит.

Новый handshake использует OS CSPRNG. Повтор стадии возвращает закэшированные
байты. Нет возобновления ключей/nonce после process restart. TX/RX CipherState
сериализованы отдельно. Replay-window меняется после аутентификации, поддельный
высокий counter не вытесняет принятые записи. Authenticated malformed frame
занимает свой counter и не обрабатывается повторно. Лимит authentication failures
1024 относится к epoch, а не к общему health провайдера. Обе стороны ограничивают
объём и число записей под ключом.

NeedsRekey/hard limits реализованы; автоматические rekey, coexistence epochs и
READY protocol ещё отсутствуют. Lifecycle защищает поколения/переходы и не
заменяет полноценное подтверждение готовности. ConfigHash не означает, что
сервер уже проверил конфиг. Отмена/истечение handshake — задача будущего actor.

## Tx/Rx

Tx отделяет одну логическую копию DATA от физических APPEND-попыток. RECEIPT
освобождает logical/account debt один раз; late completion payload не воскрешает.
Unknown commit сохраняет долг. NotCommitted возвращает DATA в Q только при
отсутствии других возможных или доказанных commits. Доказательство commit
сохраняется и после удаления записи физической попытки.

ACK допустим по границе непрерывно принятого DATA. RESET не освобождает место
в почте. Tx начинает с нулевого peer credit: необходимы абсолютные connection и
stream windows; repair не расходует unique-byte credit повторно.

Rx считает рост highest offsets, включая holes. Delivery растёт по фактически
успешным socket bytes. SHA-256 исходного DATA и его границы/logical ID позволяют
распознать дубликат после partial write, не храня выданный prefix. Конфликты
отвергаются; полностью выданные старые диапазоны игнорируются без новой записи.
Этот hash проверяет неизменность уже аутентифицированного DATA, не заменяет AEAD.

Хвост после partial write копируется, прежний буфер очищается: prefix не удерживает
весь backing array при пополненном receive credit. Peek/Frame дают bounded copies.
**Общего RSS/encoded-buffer allocator и worker queues ещё нет**. Логический лимит
не доказывает RSS ≤12/16 MiB; временные копии, ciphertext, MIME и sockets нужно
учесть отдельно при интеграции.

FIN ждёт delivery до final offset. RESET учитывает final один раз. Реестр закрытых
потоков обязателен: сами Tx/Rx не запрещают повторный OpenStream после удаления.
Registry допускает перестановку OPEN, объединяет нечётные IDs в интервалы,
резервирует место для tombstone при OPEN. При нехватке метаданных блокируется
новый OPEN; сведения о старых закрытых потоках не выбрасываются.

## Общий пул и восстановление

Governor — сериализованная модель. Все accounts одного provider. User share
защищена на каждом аккаунте, сумма долей ≤account data limit. Эти лимиты исключают
будущий отдельный bootstrap/control reserve. Тест: два пользователя, каждый имеет
доступ ко всем десяти аккаунтам. Реквизиты реальных аккаунтов не использовались.

Issue резервирует весь limit. Usage кумулятивен. Close — окончательное
authenticated обещание прекратить новые записи; final включает pending/unknown.
Освобождается только unused. Clean поступает от координатора после проверки
конкретных owned messages, не из непроверенного client counter. Retired token
возвращает ErrStale. Timeout, silence или RECEIPT квоту не возвращают.

**Journal, удалённые client permits, суммарные IMAP connections/writers/rate limits
и GC proofs ещё отсутствуют.** Нельзя публиковать грант до durable intent или
создать пустой Governor после рестарта и считать прежние unknown APPEND исчезнувшими.
Это должен обеспечивать будущий journal/reconciliation.

DRR пользователей: quantum 128 KiB, cap 512 KiB, blocked/idle не копит deficit.
Actor подаёт только eligible DATA cost ≤32 KiB и атомарно резервирует выбранный
ресурс. Вторая ступень между потоками/account scoring пока не подключены.

Watch генерирует STATUS probe, не DATA retry. Repair допускается по новому NACK
с лимитом трёх попыток вместе с исходной; после лимита ждёт deadline.
Детектор holes и реальный STATUS/NACK exchange через почту ещё предстоят.

## Проверки и следующие gates

Подготовлены 32 top-level Go tests + 2 fuzz targets. Интеграционный тест использует
реальные wire/Noise/flow; 16 seed задают потери/порядок событий. Отсутствующий
диапазон в harness задаёт тест — он пока не обнаруживается через IMAP.
44 Python-теста выполнялись здесь: 32 прежних launcher regression и 12 новых
проверок Go JSON gates, bootstrap, dependency pins, stale reports/lock, wire fixture.

До готовности transport: локальный Go test/vet, реальный go.sum, race detector,
длительный fuzz, полные control codecs, actor property campaigns, crash/journal
tests, vulnerability audit, IMAP fixtures/provider interoperability, TCP/DNS/TUN
E2E и двухпользовательский Rambler soak. Ни один из этих gates не считается
пройденным на основании Python checks.

## Сверенные первичные источники

- https://pkg.go.dev/github.com/flynn/noise@v1.1.0
- https://go.dev/dl/?mode=json
- https://pkg.go.dev/golang.org/x/crypto@v0.57.0
- https://pkg.go.dev/golang.org/x/sys@v0.48.0

Сверка API/версий не заменяет компиляцию или аудит криптографического протокола.
