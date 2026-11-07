> Historical core3 local-writer contract. In core4, `REMOTE_GRANTS_V1.md` adds
> remote grants in the same budget and schema-1 → schema-2 migration. Existing
> local API guarantees remain; remote records cannot use local writer APIs.

# ITOM — durable local-writer journal v1

Ревизия core-3-journal-1, source 0.5.0-dev3. Реализует часть milestone 3.1:
персистентный учёт локальных физических APPEND-попыток. Это не готовый IMAP
адаптер, распределённый WRITE_GRANT protocol или серверный daemon.

## Владелец и данные

Один Store соответствует одному endpoint-процессу и открывается через bbolt
с exclusive file lock и ограниченным временем ожидания. Конфигурация содержит
account IDs, один provider на пул, UID пользователей, доли bytes/message slots
и точные имена принадлежащих им папок. Десять аккаунтов могут одновременно
использоваться обоими пользователями; разделения на пять плюс пять нет.

Metadata store содержит:
- schema, случайный store ID, монотонные run/grant/attempt IDs;
- canonical configuration, чтобы смена конфига не обнулила старый долг;
- local grants, used/released amounts, closed marker;
- intents: account/user/session, folder, batch ID, encoded/decoded length,
  SHA-256 canonical decoded mail body, state, наблюдённые UIDVALIDITY/UID;
- shared account/user physical balances и per-user metadata counters.

Payload, PSK, пароли IMAP, Noise keys и TCP stream state в базу не передаются.
Новый runtime после crash должен создавать свежую сессию, не обещая продолжение
старых TCP-соединений. Слово «восстановление» здесь относится к ownership и
exposure, а не к повторной выдаче приложению старых пользовательских bytes.

Конфигурация journal immutable. Несовпадение при Open возвращает ошибку;
база не удаляется и не переинициализируется. Online provisioning/изменение
account registry потребует отдельной транзакционной миграции, которой пока нет.

## Транзакционная последовательность

1. После Open runtime выполняет bounded mailbox inventory по account/user.
   Только доверенный локальный scanner может вызвать EnableAfterInventory с
   текущим Run. Сам Open/Audit не означает, что почтовый сервер проверен.
2. Issue резервирует local grant одновременно в общем account balance и доле
   пользователя. Выданный, но не потраченный grant остаётся долгом.
3. Prepare атомарно расходует bytes/message slot гранта и сохраняет intent.
   EncodedBytes — весь хранимый MIME объект, а не только DATA payload. Каждый
   physical repair проходит новый Prepare; логический receipt не даёт квоту.
4. Start атомарно переводит Prepared в Started. Только успешное возвращение
   Start разрешает одному worker начать APPEND. Повторный Start отвергается.
5. Complete фиксирует Accepted или Uncertain. RejectedBeforeCommit допустим
   только непосредственно из Started при доказанном отсутствии записи.
6. Observe проверяет digest/decoded size, account, exact folder, UIDVALIDITY/UID
   полного FETCH. Marker/Message-ID недостаточно для установления ownership.
7. Deleted принимает доверенное локальное подтверждение удаления/отсутствия
   именно наблюдённого UID в той же текущей UIDVALIDITY. Удаление intent,
   освобождение physical quota и metadata counters атомарны.

CloseGrant освобождает лишь неиспользованную часть local grant. Все Prepared,
Started, Unknown и Committed intents продолжают занимать своё место. Безопасность
этого правила основана на том, что ВСЕ локальные APPEND сначала проходят Prepare
и Start, а файл принадлежит одному процессу. После CloseGrant уже подготовленный
intent того же Run может получить Start: его расход ранее сохранён в Used.

CancelPrepared отменяет intent, ещё не получивший разрешение на I/O, включая
старый Prepared после restart. Started/Unknown отменить этим методом нельзя.

## Сбой и неопределённость

NoSync=false и NoGrowSync=false; DB и Options не выдаются наружу. На Unix после
Open синхронизируется также parent directory. Windows file locking/fsync
дополнительно проверяется локальным core gate; native Android ещё не проверен.
Используется локальная файловая система и private directory, не network share.

Любая неожиданная ошибка записи или чтения переводит объект Store в fail-stop:
последующие операции возвращают JOURNAL_IO_UNCERTAIN до закрытия/повторного Open.
Ошибки проверки входа/квоты откатывают транзакцию и не отравляют Store. Результат
транзакции не выдаётся вызывающему коду при ошибке даже после возможного commit.
Нельзя автоматически повторить Issue/Prepare/Start после такой ошибки: нужно
перечитать inventory. Иначе потерянный completion мог бы создать второй APPEND.

На каждом Open Run увеличивается, admission gates сбрасываются. Старые Started
транзакционно переходят в Unknown; перезапуск не уменьшает ни один balance.
Старый grant не принимает новых intents. Старый writer completion/Start не
может получить разрешение нового Run. Старые неопределённые попытки доступны
текущему scanner для Observe/Deleted, но не для повторного APPEND.

После retire запись удаляется, но монотонные ID не переиспользуются; store ID
защищает от alias после замены DB. Повторный terminal вызов возвращает
JOURNAL_RETIRED_OR_UNKNOWN без изменения counters. Runtime обрабатывает такой
ответ как отсутствие нового эффекта, а не как разрешение повторить запись.

Отсутствие неизвестного письма в одном SEARCH, timeout, RECEIPT или только флаг
\\Deleted НЕ освобождают quota. Если невозможно доказать окончательный исход,
консервативный долг сохраняется до последующей сверки. Политика безопасного
закрытия retired namespace и исчерпания reconciliation deadline ещё предстоит.

## Границы доказательства очистки

Observe/Deleted — внутренние доверенные API, не обработчики произвольного
сетевого запроса. Будущий IMAP worker обязан сопоставлять завершение команды
с актуальной connection/mailbox generation и UIDVALIDITY. Journal проверяет
переданный locator относительно сохранённого; он не может сам установить,
какая UIDVALIDITY сейчас на IMAP-сервере, потому что не делает сетевых операций.
После изменения UIDVALIDITY нужна новая полная проверка содержимого через
Observe. Другой UID той же incarnation для уже наблюдённой попытки отвергается.
Дублированные провайдером физические письма требуют отдельной reconciliation
политики; один matching marker не разрешает массовое удаление.

## Ресурсы и исполнение

MaxGrants и MaxAttempts задают bounded metadata admission (каждый максимум
65536). Метаданные делятся фиксированными защищёнными долями по уникальным UID
во всём пуле; один пользователь не может занять долю другого. Остаток от деления
не раздаётся. Account/user bytes и message slots имеют собственные лимиты.
Настройки должны оставлять отдельный бюджет bootstrap/control; этот DATA
journal его не создаёт и не расходует.

Canonical config <=1 MiB, JSON одной записи <=4096 bytes, decoded batch <=1 MiB,
encoded MIME <=2 MiB. ScanGrants/ScanAttempts отдают максимум 256 копий, без
удержания bbolt transaction у вызывающего кода. Audit ограничен metadata caps
и пересчитывает balances и references, включая used−released=live attempts.
Неисправные balances, неизвестная schema, неподходящий config останавливают Open.

Постоянные row/count caps не означают жёсткий предел bytes файла: bbolt повторно
использует свободные страницы, но файл автоматически не сжимается. Контроль
свободного диска и offline compaction — задачи будущего daemon. Disk full не
разрешает продолжать APPEND; ошибка commit переводит Store в fail-stop.

API синхронный и ждёт commit/fsync; вызывать его нужно в metadata worker с
bounded очередью. Session actor не должен блокироваться на этих методах.
Store сериализует операции, что проверяется race detector. Производительный
batch/group commit, latencies fsync и измерения Android energy ещё предстоят.

## Локальные и сетевые гранты

Journal Grant — локальная allocation writer-у, у которого все intents проходят
через эту базу. Его НЕЛЬЗЯ отправлять клиенту как готовый WRITE_GRANT и затем
применять CloseGrant, предполагая, что Used содержит неизвестные remote APPEND.

`pool.Governor` остаётся отдельной проверенной моделью удалённых грантов. Нельзя
выдать два независимых полных account budgets — один через Governor, другой
через Store. Следующий шаг распределённого coordinator должен объединить
remote outstanding allocations и server-local reservations в одном durable
account ledger, определить grant epoch/usage/release/reconciliation и wire
контракты. До этого WRITE_GRANT/GC wire-типы остаются CONTROL_UNSUPPORTED.

## Проверки этой ревизии

Добавлены 15 Go Test functions (одна — subprocess helper), включая:
- restart с открытым grant и независимый admission второго пользователя;
- оба пользователя на всех 10 аккаунтах;
- single Start, защита старого Run, неизвестный исход без освобождения debt;
- digest/namespace/UIDVALIDITY mismatch, запрет unproved cleanup;
- transaction rollback и lost acknowledgement после commit;
- concurrent reservations, protected metadata shares, cursor bounds;
- lock второго процесса, config mismatch, corruption of counters;
- процесс завершён через os.Exit без Close до/после Start;
- отсутствие user payload в metadata DB.

На Linux/amd64, Go 1.27.1 выполнен полный gate с race detector: **8 packages,
144 passed test events, 0 failed/skipped, vet passed**. 144 включает subtests
и fuzz seeds; всего source содержит 66 Test functions и 3 fuzz targets.
Тесты subprocess exit и injected lost completion не являются физическим power
cut испытанием файловой системы. IMAP/Android/soak/throughput не проверялись.

Отчёт, events, dependency snapshot и hashes точных core/tools источников:
`reports/core3-linux-20260925/`. Полный pinned module graph содержит 21 module;
это не значит, что все 21 компилируются в runtime. В go.sum 31 запись.

## Зависимость

Закреплён `go.etcd.io/bbolt v1.4.3`; это выбранная версия, не заявление о latest.
Суммы взяты из официальной Go checksum database и затем проверены Go tooling.
Noise/x/crypto/x/sys pins сохранены. go.mod/go.sum скопированы из результата
настоящего mod tidy/verify в изолированной копии, а не составлены вручную.

Первичные источники API/версии, проверенные при реализации:
- https://github.com/etcd-io/bbolt/releases
- https://github.com/etcd-io/bbolt
- https://pkg.go.dev/go.etcd.io/bbolt
- https://proxy.golang.org/go.etcd.io/bbolt/@v/v1.4.3.mod
- https://sum.golang.org/lookup/go.etcd.io/bbolt@v1.4.3
