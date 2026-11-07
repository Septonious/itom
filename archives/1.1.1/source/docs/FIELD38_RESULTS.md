# FIELD38 — исправление Windows core gate

Ревизия: `transport-s1-perf5-field17`. Архив: `ITOM_TRANSPORT_FIELD38_SOURCE.zip`.

## Причина отказа FIELD37

Все три приложенных файла пользователя прочитаны с локальных attachment paths. Windows/amd64, Go1.27.1, исходники FIELD37 SHA-256 03bb36ac48286d973392bbcda76bdffbb3374d2307de6fed143ebbcfbf4d0f02.

Пакет tunnel начал выполнение в 18:32:03.476+03:00 и аварийно завершился через 120.339s. Текущий TestMobileBaselineNoopScopeAndCancellation стартовал только в 18:34:00.218+03:00: ему досталось 3.597s. Стек main теста находится в waitLane при создании production supervisor fixture; нет сообщения о провале NOOP/cancellation assertion. Go пишет `panic: test timed out after 2m0s`, running test `(3s)`.

До аварии 130 tunnel test events завершились PASS, два optional SKIP; завершённых assertion FAIL нет. Это не успешный прогон: поздние тесты не выполнились, и строгий core gate корректно отклонил архив. Поле failed_tests=[] не означает PASS — процесс пакета оборван и текущий test event остался UNFINISHED.

Go `-timeout` ограничивает весь test binary пакета, не каждый отдельный тест. Среди уже завершённых сценариев Windows: upload23.10s, RTT21.91s, четырёхаккаунтное ожидание17.08s, mixed HTTP11.74s. Предыдущий Linux FIELD37 пакет занял117.795s: общий лимит120s имел слишком маленький запас, что является ошибкой подготовки FIELD37.

## Исправление

В tools/build_core.py normal package timeout изменён120s→300s; race уже имел300s. Лимит остаётся конечным. Не изменены числовые критерии скорости/RTT, внутренние deadlines сетевых операций, проверка30ms отмены NOOP, cancellation upper bound150ms, тестовое ожидание17s, набор тестов, проверка полного набора пакетов и completeness test events. Ничего не удалено и не skipped ради успеха.

Новый package netroute остаётся в строгом списке EXPECTED_PACKAGES. Транспорт, Android, native и тесты FIELD37 сохранены. Сравнение файлов core/native/android показывает только три Go изменения строк ревизии в cmd/itom, manager/trace, tunnel/trace; native6files и Android43files идентичны FIELD37. Исправления клиентской DNS/сетевой привязки и pre-auth Mail.ru сохраняются; подробности в docs/FIELD37_RESULTS.md. Здесь не менялись scheduler, grants, wire/capabilities, TLS или политика аккаунтов.

## Финальные проверки FIELD38

| Проверка | Результат |
|---|---|
| Stock normal Go gate | 437 PASS,0FAIL,4optionalSKIP;19packages |
| Stock race Go gate | 437 PASS,0FAIL,4optionalSKIP;19packages |
| normal tunnel package | 121.572s при бюджете300s |
| race tunnel package | 146.654s при бюджете300s |
| MobileBaseline normal / race | PASS за5.06s / 5.06s |
| Bundled IMAP fork race | PASS |
| go vet | PASS |
| Linux amd64 / arm64 | build PASS |
| Native Go normal / race | PASS / PASS |
| Windows amd64 tunnel test binary | crosscompile PASS; Windows runtime НЕ выполнен |
| Python launcher / core tools | 34 /30 PASS |
| Offline source check | 8Python,11XML,source-set boundaries PASS |
| APK / Android lint /56Kotlin tests /device | Не выполнены |
| Реальный Mail.ru /speedtest /RTT | Не выполнены после исправления |

Полный normal пакет в этом прогоне превысил даже старые120s и всё равно закончил все проверки. Оба gate продолжают выполнять все обязательные тесты. Optional SKIP остались: TestFieldHTTPSColdStartAndDuplex130, TestLoadedApplicationProductionHarness, TestLoadedApplicationSupervisorNonOwnerRejoin, TestLoadedApplicationSupervisorOwnerSurvival. Длительная fuzz campaign не выполнялась; штатный seed corpus выполнен.

code_gate_passed=true; actual_windows_runtime_tested=false; apk_built=false; live_provider_tested=false; field_performance_qualified=false. Нового измерения75Mbps/RTT<1000ms нет. Синтетический core gate не объявляется доказательством реальной работоспособности. Rambler в текущую полевую проверку не включать. Пользовательскую конфигурацию, аккаунты и журналы не стирать.

## Команды

Windows PowerShell:

```powershell
python .\ITOM_TRANSPORT_FIELD38_SOURCE.zip all --expect-revision transport-s1-perf5-field17 --accept-android-licenses
```

VPS amd64 после загрузки itom-linux-amd64 в /tmp:

```bash
chmod 755 /tmp/itom-linux-amd64 && sudo /tmp/itom-linux-amd64 install
```

Установить также сгенерированный APK FIELD17: именно клиент содержит исправленный Android сетевой путь FIELD37. Если после успешной сборки соединение не установится, экспортировать новые диагностические архивы обеих сторон. Дополнительная read-only VPS проверка `itom imap-check --account a01` остаётся доступной.
