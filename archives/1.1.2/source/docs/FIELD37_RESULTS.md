# FIELD37 — результаты исправления запуска Mail.ru

Ревизия: transport-s1-perf5-field16. Архив: ITOM_TRANSPORT_FIELD37_SOURCE.zip.

## Реальные входные данные

Прочитаны два локальных архива пользователя от 2026-10-06 15:04/15:06 UTC; их содержимое не распространяется вместе с исходниками. Сервер FIELD15, клиент FIELD14; оба имеют совместимый wire ITH9/capabilities. Четыре аккаунта a01–a04, один пользователь. Rambler в этом сценарии не используется.

Последний server run 1791298866388291681: 2430 событий за 111s, 20 успешных IMAP-подключений, 20 challenge APPEND, 20 IO_OR_PROTOCOL_ERROR, ни одного live packet_rx. После успешных TLS/LOGIN каждая линия повторяет SELECT пустого receive mailbox примерно раз в 0.4s и обрывается примерно через 16s, затем переподключается. Показанные select_commands=0 в старом pre-auth trace — ошибка счётчика, который смотрел только на ещё не созданный dedicated reader. Page на базовом сокете действительно вызывает SELECT.

Последний client run 1791298903090644958: 6 событий, все четыре аккаунта получают IMAP_CONNECT_FAILED ровно через 20s; успешных imap_connected, TLS/LOGIN и peer packets нет. То же повторяется в двух предыдущих запусках. Старый клиент не содержит безопасных connect stage/reason. Это не доказательство ошибки grants, transport handshake, пароля или несовместимого wire: TCP/DNS путь ещё не завершён. Точная причина клиентского отказа DNS vs TCP и серверного закрытия не установлена. Repeated SELECT pressure подтверждена кодом и тестом; причина rate limit реального Mail.ru НЕ подтверждена.

## Изменения

- Android использует Network.getAllByName выбранной non-VPN сети. Прежний Go resolver пытался только первый DNS: успешное UDP Dial не доказывало ответ сервера и не переводило query к следующему DNS. Новый путь использует системное разрешение сети, включая её Private DNS, без хардкода провайдерских IP.
- Перед TCP connect: protect(fd), затем Network.bindSocket через дубликат ParcelFileDescriptor. DNS и новые сокеты следуют выбранной исходной сети. При потере validated сети новые попытки выбирают доступную validated сеть. Активные соединения сохраняют собственную привязку; это не обещание бесшовного handover.
- core/netroute поддерживает ограниченную параллельную попытку второго IP-семейства через 300ms, отмену проигравшей попытки и закрытие лишнего сокета. IP-literal не требует lookup, tcp4 не выбирает IPv6. Один бюджет 20s включает lookup и TCP. Блокирующие platform lookup ограничены восемью одновременными вызовами; ожидание отменяемое. Android getAllByName сам не отменяется Go-контекстом.
- TLS SNI/сертификат остаются привязаны к исходному имени провайдера. Пароли, папки, grants, replay fencing, лимиты physical debt и scheduler не ослабляются.
- До Activate применяется IDLE/DONE на существующем base socket и bounded UID tail. Завершение DONE синхронное, APPEND не пересекается с IDLE. UIDVALIDITY проверяется SELECT раз в 2s; без IDLE — polling через 500ms. Recovery inventory остаётся прежним, receive FETCH не превышает 16 UID.
- До создания reader счётчики читаются с base socket. imap_error/fetch_page failure содержит безопасный reason. IMAP decoder сохраняет EOF/тип ошибки через %w; текст провайдера не публикуется. Connect failure различает socket protection отказ.
- В stock launcher добавлен новый ожидаемый пакет netroute. Gate продолжает проверять полный набор пакетов, каждое test event, module pins и IMAP fork manifest.

## Выполненные проверки

| Проверка | Результат |
|---|---|
| Stock server --race, обычный Go | 437 PASS, 0 FAIL, 4 optional SKIP, 19 пакетов |
| Stock server --race, Go race | 437 PASS, 0 FAIL, 4 optional SKIP, 19 пакетов |
| Bundled IMAP fork race | PASS |
| go vet | PASS |
| Linux amd64 / arm64 | PASS |
| Native Go module, normal / race | PASS / PASS |
| Windows amd64 netroute test binary | crosscompile PASS; запуск на Windows не выполнялся |
| Python launcher / core tools | 34 / 30 PASS |
| Offline source check | 8 Python, 11 XML, source-set boundaries PASS |
| Kotlin tests | 56 присутствуют; не выполнены |
| APK, Android lint, Android устройство | Не выполнены в этой среде |
| Реальный Mail.ru / скорость / RTT / soak | Не выполнены после исправления |

Новый test TestFieldFourAccountsWaitForDelayedClientWithoutSelectFlood держит четыре реальных TLS IMAP sockets без клиента 17s, после чего запускает оба конца handshake и требует завершения всех четырёх на исходных сокетах. Normal и race прошли: по 10 SELECT и 9 FETCH за ожидание на каждом аккаунте. Нет отключений аккаунтов. В negative-control копии восстановлены старые 100ms polling / SELECT Page; ТОТ ЖЕ тест падает: по 170 SELECT на каждом аккаунте. Это намеренный FAIL старого поведения в select-before.log, не отказ финального кандидата. Модель не выдаётся за почтовый backend Mail.ru, ограничение <=12 SELECT является защитой от клиентского flood, а не объявленным лимитом провайдера.

TestPlatformDNSProtectedDualStackAndCancellation использует настоящий TCP listener, искусственно блокирует IPv6 ControlContext до отмены, требует защищённый IPv4 fallback без ожидания полного тайм-аута, отмену losing family, IP-literal bypass DNS и сохранение DNS timeout stage. Прошёл normal и race. Добавлены всего две верхнеуровневые тестовые функции; существующая функциональная защита не удаляется ради уменьшения числа событий.

Необязательные skipped: TestFieldHTTPSColdStartAndDuplex130, TestLoadedApplicationProductionHarness, TestLoadedApplicationSupervisorNonOwnerRejoin, TestLoadedApplicationSupervisorOwnerSurvival. Они не запускались в этом этапе исправления соединения. Seed corpus fuzz выполнен штатным Go gate; длительная fuzz campaign не выполнялась.

## Статус и следующий полевой прогон

code_gate_passed=true; real_connection_restored=false; field_performance_qualified=false. Ни скорость >=75Mbps, ни RTT<1000ms на реальном провайдере этой сборкой ещё не доказаны. Исторические FIELD35 model результаты не перенесены как новые результаты.

Установить APK FIELD16 и сервер FIELD16, проверить профиль только с четырьмя Mail.ru. Если соединение не установится, экспортировать новые архивы клиента и сервера. В FIELD16 будут видны connect stage/reason и причина закрытия после LOGIN. Read-only `itom imap-check --account a01` остаётся доступным на VPS. Не удалять рабочие аккаунты/профили/журналы для обхода отказа.

Команда сборки Windows:

```powershell
python .\ITOM_TRANSPORT_FIELD37_SOURCE.zip all --expect-revision transport-s1-perf5-field16 --accept-android-licenses
```

После копирования itom-linux-amd64 в /tmp VPS:

```bash
chmod 755 /tmp/itom-linux-amd64 && sudo /tmp/itom-linux-amd64 install
```

Системные API сверены с официальными Android документами: https://developer.android.com/reference/android/net/Network , https://developer.android.com/reference/android/net/VpnService , https://developer.android.com/reference/android/os/ParcelFileDescriptor .
