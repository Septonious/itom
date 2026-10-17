# ITOM FIELD39 FIX1 — исправление сборочного теста

Архив `ITOM_TRANSPORT_FIELD39_FIX1_SOURCE.zip`; ревизия транспорта остаётся `transport-s1-perf5-field18`.

Причина отказа `all` в присланном Windows-прогоне: новый `DomainTest.invitationLinkAndMultilineLint` применял `assertEquals` к двум экземплярам обычного класса `Invitation`. Этот класс, как и вложенный `MailCredential`, не переопределяет `equals`: импорт создаёт новый объект, поэтому проверка ссылочного равенства неизбежно падала независимо от одинаковых полей. Redacted toString объясняет одинаковый вид expected/actual в сообщении.

FIX1 сравнивает JSON-деревья полного сериализованного приглашения после импорта ссылки и форматирования. Проверяются все поля, включая credentials и PSK, но assertion печатает только постоянное сообщение. Дополнительно проверяется, что импорт создаёт отдельный объект. Сохранены проверки некорректного UTF-8, Base64 padding, дублирующихся ключей и строки/столбца ошибки. Тест не отключён, критерии не ослаблены. Production `equals`/импорт не менялись ради теста.

Изменены только DomainTest.kt, имя архива в source-info.json и документация. Go/core, native, production Android и Gradle-конфигурация байт-в-байт совпадают с FIELD39. Подробный список/хэши находятся в логах. Серверная ревизия field18 намеренно сохранена; нового серверного изменения или замера скорости нет.

## Фактически выполненные проверки

| Проверка | Результат |
|---|---|
| Локальная компиляция всех domain production и test Kotlin файлов | PASS, Kotlin 2.2.10, serialization plugin 2.2.10, JVM target17 |
| Оба domain test класса через JUnit4.13.2 на JDK17 | **57/57 PASS, 0 FAIL, 0 SKIP** |
| Зависимости domain | serialization-json/core1.11.0, coroutines-core/test1.11.0; stdlib2.3.20 по зависимости serialization |
| Python launcher / core-tools | 34/30 PASS |
| Offline source/XML | PASS, 8 Python и11 XML |
| Сравнение production с архивом FIELD39 | Все файлы core/native/production Android идентичны |

Kotlin/JVM проверка выполнена реальным компилятором и обеими JUnit test classes напрямую; это не повторный Gradle `all` и не сборка APK в локальной среде. Эта разница отражена в отчёте. JUnit использует тот же kotlin-test-junit и версии domain зависимостей из проекта.

## Подтверждения из твоего Windows-прогона FIELD39

Для точного FIELD39 SHA256 `347b300a97c8f2f687439ecd2757264528b307e17d49531b10992642af74cd0c`:

- Go core gate PASS:19 пакетов,444 PASS test events,0 FAIL,4 прежних optional SKIP; IMAP fork, vet и Linux amd64/arm64 сборки PASS. Race в этом Windows-прогоне не запускался.
- Native gomobile bind для Android arm64/arm PASS.
- `compileDebugKotlin`, `compileReleaseKotlin`, `assembleDebug` PASS; lint0 ошибок/3 предупреждения.
- Domain57 тестов:56 PASS,1 FAIL на ошибочном сравнении Invitation. Общий `all` корректно отклонён. FIX1 устраняет именно этот отказ.

Обычный Go gate не перезапускался локально для Kotlin test-only изменения: весь Go/native код идентичен проверенному архиву. Полный Linux normal/race FIELD39 был ранее успешен, по444 PASS/0 FAIL; эти исторические результаты не называются новым запуском FIX1. Предупреждения SDK XML, allNetworks deprecation и3 lint warnings не являлись причиной отказа. Unrelated UI/manifest/icon изменения в FIX1 не добавлялись.

## Команды

```powershell
python .\ITOM_TRANSPORT_FIELD39_FIX1_SOURCE.zip all --expect-revision transport-s1-perf5-field18 --accept-android-licenses
```

```bash
chmod 755 /tmp/itom-linux-amd64 && sudo /tmp/itom-linux-amd64 install
```

Если field18 уже установлен на VPS, серверный бинарник FIX1 тот же. Для полевой проверки исправлений FIELD39 установить новый собранный APK. Реальная стабильность провайдеров/первопричина физического закрытия сокетов остаётся отдельной задачей; лабораторные числа FIELD39 не объявляются реальной приёмкой.
