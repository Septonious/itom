# ITOM FIELD39 FIX2 — исправление Go-сценария лимита соединений

Архив `ITOM_TRANSPORT_FIELD39_FIX2_SOURCE.zip`, ревизия `transport-s1-perf5-field18`.

## Причина отказа FIX1

В присланном Windows Go1.27.1 прогоне:442 успешных test events,2 FAILED events (один subtest и его родитель),4 прежних optional SKIP. Пакет tunnel завершился за206.972с, общий300с таймаут не исчерпан. Исходная ошибка: `provider cap did not actually degrade DATA capacity context deadline exceeded`. Сообщения wsarecv/wsasend после неё относятся к закрытию fixture и сами по себе не доказывают новый отказ транспорта.

Старый HTTP-тест предполагал, что cap9 обязательно уменьшит число DATA writers. Но server workers открывают IMAP-сокеты по требованию, а загрузка разных writers зависит от расписания. Лимит также может отказать optional cleaner вместо DATA. В этих допустимых порядках обе стороны сообщают DATA capacity2, и тест ждёт искусственно ожидавшегося отказа до deadline. На Linux старый сценарий отдельно прошёл три повтора; это подтверждает зависимость от запуска, но не отменяет Windows failure.

## Исправление

После активации четырёх исходных carriers тест занимает свободные места до cap9 настоящими authenticated TLS/IMAP sessions, моделируя дополнительного почтового клиента. Запускает HTTP, подтверждает именно новое снижение серверной DATA capacity и новый отказ dial во время HTTP, освобождает дополнительные sessions, поднимает cap10 и ждёт capacity2 на всех исходных линиях. Восстановление происходит без повторного запуска HTTP requests или carrier.

Сохранены проверка полных байтов HTTP/2 upload/download,8 fresh HTTP/1.1 запросов, короткий запрос во время bulk, неизменность logical session/generation и carrier identity, контроль peak<=10 и отсутствие неожиданных carrier exits. Таймауты30/40с не увеличивались. Тесты не удалены и не переведены в skip. Production transport/core/native/Android не менялись, ревизияfield18 сохранена. Исправление Kotlin-теста Invitation из FIX1 включено.

Краткий сборочный отчёт больше не заменяет исходную причину последними supervisor health counters. Новая Python-регрессия воспроизводит20 таких строк после timeout; исправленный extractor на полном пользовательском JSONL тоже сохранил именно timeout assertion.

## Фактически выполненные финальные проверки

| Проверка | Результат |
|---|---|
| Stock server Go gate, normal,19 пакетов | 444 PASS /0 FAIL /4 прежних optional SKIP |
| Тот же полный Go gate с race detector,19 пакетов | 444 PASS /0 FAIL /4 optional SKIP |
| IMAP-fork с race / go vet | PASS / PASS |
| Linux amd64 и arm64 | Обе сборки PASS; amd64 version=field18 |
| Финальный HTTP-сценарий при GOMAXPROCS=1 | 3 повтора, оба subtests PASS |
| Финальный HTTP-сценарий с diagnostics/observedMail | Оба subtests PASS |
| Native Go-модуль normal / race | PASS / PASS |
| Все domain Kotlin production+test sources и JUnit | Компиляция PASS, **57/57 PASS**,0FAIL/0SKIP |
| Python launcher / core-tools | 34/31 PASS |
| Offline XML/source boundaries | PASS,8Python/11XML |
| Windows tunnel test binary | Cross-compile PASS; Windows execution локально не проводилось |
| Production comparison с FIX1 | 227 production files byte-identical; изменения только в Go test, build-report tooling и документации |

Дополнительные четыре optional Go-сценария (field HTTPS130, production harness, owner survival, non-owner rejoin) не включались в этом сборочном исправлении; они остаются прежними явно optional tests. Новых skip нет. Fuzz выполнен только на seed corpus штатного gate.

Kotlin проверялся прямым компилятором2.2.10/serialization plugin2.2.10 + JUnit4.13.2 на JDK17 с serialization1.11.0/coroutines1.11.0. Это реальный запуск57 тестов, но не локальная Gradle/AndroidAPK/device сборка. Production Android и Gradle-конфигурация совпадают с FIELD39, для которого пользователь ранее подтвердил compileDebug/ReleaseKotlin, assembleDebug и lint0errors/3warnings. FIX2 не объявляется новым успешным Windows `all` до пользовательского запуска.

Нового замера скорости/RTT и подключения к Mail.ru/Rambler нет. Физический массовый обрыв из полевых трасс всё ещё требует проверки на устройстве; успешный Go gate не является подтверждением реальной75Мбит/с приёмки.

## Две команды

```powershell
python .\ITOM_TRANSPORT_FIELD39_FIX2_SOURCE.zip all --expect-revision transport-s1-perf5-field18 --accept-android-licenses
```

```bash
chmod 755 /tmp/itom-linux-amd64 && sudo /tmp/itom-linux-amd64 install
```

Если VPS уже наfield18, production серверный код этого архива тот же. При полевой проверке нужны собранный APK и соответствующий серверfield18.
