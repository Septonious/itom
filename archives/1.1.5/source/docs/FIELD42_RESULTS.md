# ITOM FIELD42 — результаты исправления запуска

Ревизия: `transport-s1-perf5-field21`. База — точный FIELD41, транспортный двигатель/RTT pacing из рабочего FIELD40 сохранены.

В новых логах FIELD41: клиент90 и сервер122 отказа `IMAP_SCOPE_FETCH_MISSING_BODY` во время recovery, на10/10 аккаунтов. IMAP login проходит. Нет packet/frame traffic в этих новых запусках: сбой появляется до нагрузки. Старые field17/field19 записи в тех же архивах исключены из подсчёта.

Доказанный дефект кода: парсер BINARY не читал допустимое смещение `<0>` после `]`. Raw-peer ответ с RFC822.SIZE, HEADER, затем BINARY[1]<0> на старом FIELD41 воспроизвёл точный код ошибки из логов. Исправленная версия проходит этот сценарий. Трасса не содержит реального wire-ответа провайдера: его точная форма не подтверждена.

Внесено: правильный разбор и проверка BINARY origin/part; отдельное отслеживание BINARY при завершении UID; физическое BODY-чтение для recovery/inventory/exact checks; один read-only BODY fallback после неполного завершённого BINARY. APPEND остаётся base64, конфигурация и журналы не сбрасываются.

| Проверка | Верхнеуровневых случаев | Подслучаев | Результат |
|---|---:|---:|---|
| Mailbox, normal |12|25|PASS|
| IMAP decoder UID updates, normal |1|6|PASS|
| Startup/recovery/NOOP/stream wrapper, normal |4|0|PASS|
| HTTP/2 mixed traffic, nominal+BINARY |1|1|PASS|
| Mailbox, race |12|25|PASS,0 data races|
| Startup/recovery/NOOP/stream wrapper, race+BINARY |4|0|PASS,0 data races|

Итого18 normal и16 race верхнеуровневых проверок, финальных ошибок0. Подслучаи отдельно, не добавляются к числу независимых проверок. До исправления воспроизводящий тест ожидаемо FAIL; этот результат сохранён отдельно и не засчитывается как PASS.

Сценарии raw-peer: HEADER first / BINARY first; `<0>` / отсутствие origin; пропущенный BINARY / HEADER; NIL / пустое decoded body; отклонение неверного origin / части; recovery только BODY. Real TLS сценарии: canonical binary / UNKNOWN-CTE / generic NO / rewritten header; обычное recovery-чтение и scoped deletion с сохранением физического MIME.

Production supervisors с объявленным BINARY и старым профилем8MiB/128:10/10 линий имеют bulk+fast grants, на каждой прошёл реальный TCP echo. Normal и race passed. HTTP nominal:4 здоровых линии,0 самопроизвольных отключений, epochs preserved; HTTP/2 upload4718592 и download4718592 байт,8/8 коротких запросов завершились во время bulk. Это проверка работоспособности и целостности, не скорости.

`go vet` затронутых mailbox/tunnel/CLI:PASS. Сборка Linux amd64/arm64:PASS. CLI amd64 сообщает field21. Go-код native для Android arm64 с CGO=0 компилируется:PASS; это не JNI/AAR/APK.

Полная историческая Go-suite, fresh throughput benchmark, реальные подключения Rambler/Mail.ru, Windows execution, Android Gradle/unit/lint/device не выполнялись.100+Мбит/с не обещаются. После обновления APK и VPS требуется новый полевой запуск. FIELD40 остаётся рабочей пользовательской базой; FIELD41 полевую проверку не прошёл.

Подробные JSONL, команды, исходные Go SHA256 и воспроизведение до исправления: `ITOM_FIELD42_TEST_LOGS.zip`. История/объяснение: `docs/FIELD42_STARTUP_FIX.md` в исходниках.
