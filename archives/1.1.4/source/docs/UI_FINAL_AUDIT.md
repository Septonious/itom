# Аудит завершения UI: 0.4.0-dev4 / ui-final-1

Дата: 2026-09-24. Основа — точный source ZIP native-ui-2, SHA-256 `7cca49ab7391b7c4ffc52a13d72bbc09a47c76f4feb6aa5220a37d0526337936`; исходный manifest проверен перед изменениями. Запрос: убрать ненужные демо-функции, назвать приложение ITOM, провести аудит, закрыть этап UI. Новый дизайн/HTML не создавались.

## Найдено и исправлено

| Находка | Изменение | Проверка и предел доказательства |
|---|---|---|
| Debug показывает вымышленные соединение и метрики, содержит тестовый foreground service | Общий UnavailableBackend для debug/release; удалены MockBackend/MockSessionService, таймер, demo/error actions, FGS manifest и permissions | Source boundaries; модель Reject покрыта Kotlin test, ожидает локального выполнения |
| Runtime demo признаки проходят через model/editor/UI/diagnostics | Удалены demo поля из текущих публичных/редакторских/session моделей; диагностика schema v2; уведомление содержит лишь системные настройки | Проверены call sites, обоим вариантам доступен один backend; native @Preview/test fixtures только для разработки |
| Удаление всех demo IDs потеряло бы уже введённые пользователем секреты | Vault v1→v2: exact pristine fingerprint + отсутствие секретов для удаления, сохранение всех изменённых профилей, incomplete status до заполнения | 9 новых Kotlin migration tests и переработанные editor tests; выполнение локальное |
| AtomicFile может не бросать исключение при некоторых sync/rename ошибках | Явный fd.sync и сравнение прочитанного ciphertext; без подтверждения нет публикации успеха, дальнейшие edits заблокированы до reload | Статический анализ транзакции и AOSP; реальный Keystore/I/O fault injection на устройстве ещё не выполнялся |
| Одинаковый select/settings увеличивал revision и мог создавать лишние конфликты | Проверка candidate == old до записи; ограничение переполнения revision | Ручной анализ веток update; повторный выбор включён в device checklist |
| Старый экспортированный APK оставался после повторной ошибки сборки | Целевые APK/sha удаляются под lock до bootstrap; очищается частичный export при ошибке | 2 новых выполненных Python-теста: failed Gradle rebuild и SDK/bootstrap failure; чужие файлы сохраняются |
| ITOM Dev в launcher/tile | Имя ITOM в обоих вариантах и QS tile; versionCode 4005 | Проверены Gradle/manifest/tile; package IDs/Keystore envelope сохранены для обновления |

Причина усиления AtomicFile проверена по [официальному исходнику AOSP](https://android.googlesource.com/platform/frameworks/base/+/HEAD/core/java/android/util/AtomicFile.java): `finishWrite` логирует некоторые ошибки синхронизации/переименования. Проверка committed bytes — дополнительная защита от ложного успеха; она не является доказательством поведения каждой файловой системы при внезапном отключении питания. После завершения finishWrite не вызывается failWrite.

## Область обзора

- Domain: schema/codec limits, strict JSON, routing/address parsing, per-app shared UID, secrets/reference isolation, profile editing, revision guards, reducer и метрики. Чистая модель не объявлена работающей сетевой фильтрацией.
- Android: навигация, фиксированная главная/орбита, полноэкранные editor/apps, storage transaction, Keystore, import/export/QR, секреты в ViewModel, FLAG_SECURE, PackageManager и ограниченный icon cache, lifecycle permission state, диагностика и tile.
- Packaging: application identity/icons/manifest, pinned Gradle/AGP/Kotlin/Compose, UTF-8 на Windows, проверка ревизии, загрузки/checksums, безопасная распаковка, cache lock, JUnit/Lint diagnostics и экспорт APK только после gates.
- Test sources, публичные fixtures, актуальные README/CONTRACTS/PHASE2_STATUS/DEVICE_CHECKLIST. Исторические FIX/NATIVE_UI документы сохранены как история прежних ревизий.

Сохранены fixed routing order, один provider на профиль, обязательная проверка TLS-сертификата, ограничения входных документов и отсутствие automatic Direct fallback. Реальная принадлежность произвольного IMAP-hostname провайдеру ещё не проверяется по доверенному серверному preset; это уже отражено в контракте pairing.

`applicationIdSuffix=.dev` сохранён намеренно: приложение отображается как ITOM, а обновление продолжает использовать прежнюю песочницу и ключ. Смена package ID потребовала бы отдельной миграции между приложениями. Для обычного обновления нужен тот же package ID и сертификат подписи; см. [Android: настройка application ID](https://developer.android.com/build/configure-app-module).

## Фактически выполнено

- 32 Python regression tests прошли: прежние 30 плюс 2 проверки устаревшего артефакта. Gradle/SDK в этих tests заменены локальными fixtures; настоящие APK не собирались.
- AST-разбор 4 Python и XML-разбор 12 Android XML; source-set invariants и отсутствие runtime demo подтверждены offline checker.
- Проверены вызовы после изменения SessionBackend/ProfileEditSession/SessionSnapshot, ресурсные ссылки, Kotlin delimiters и длины положительных hex-ID fixtures. Это статические проверки, не Kotlin compiler.
- Финальный ZIP проверяется новым запуском entry point `check --expect-revision ui-final-1`, с отключённым Python UTF-8 mode и EncodingWarning как ошибкой; manifest/hash и fresh extraction обязательны.

Подготовлены 52 Kotlin/JUnit tests (42 прежних, адаптированы 2 для incomplete вместо demo, плюс 10 новых). Здесь они не запускались. Не выполнялись Android compile/Lint/Preview/device tests, реальный IMAP/TUN, нагрузочные испытания, сборка server или production signing, dependency vulnerability scan. Наличие источников/успешных Python tests не означает успешную сборку Android.

## Итог и продолжение

Оформление принято пользователем; код UI/Android-оболочки зафиксирован, runtime демо удалено. Локальная команда выполняет оставшиеся compile/test/lint gates, а DEVICE_CHECKLIST — smoke и миграцию на телефоне. Реальное уведомление со скоростями/RTT, VPN lifecycle, DNS, IMAP и сервер относятся к следующему этапу. G2 не закрыт; 120 Мбит/с из старого проекта не приписываются ITOM.
