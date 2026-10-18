# build-fix-4: Android Lint и полная диагностика

Дата: 2026-09-23. Основание — присланный пользователем вывод PowerShell для build-fix-3. Локальная сборка APK в чате не запускалась.

## Что подтверждено журналом

| Этап | Результат |
|---|---|
| Launcher и source revision | build-fix-3; SHA-256 ee8ebc33fee5a5826de6ee016911a7a5e74864f6b38108f1fc1a3edfe26b0f59 совпадает с выданным архивом |
| Python/offline | 18 тестов прошли |
| :domain:test | Прошёл; прежние три ошибки заготовки больше не останавливают сборку |
| :app:compileDebugKotlin / :app:compileReleaseKotlin | Прошли |
| :app:assembleDebug | Прошёл; общая команда всё же завершилась неуспешно |
| :app:lintDebug | 2 ошибки, 11 предупреждений; первая — StartActivityAndCollapseDeprecated в ItomTileService.kt |
| Текст второй ошибки | В присланном выводе отсутствует |
| SDK XML v3/v4 и strip libandroidx.graphics.path.so | Предупреждения; после них Gradle продолжил работу до Lint |

Полный локальный JUnit XML и отчёт Lint пользователя не предоставлены. Число 30 — количество подготовленных Kotlin-тестов в исходниках. Успех задачи domain:test подтверждён журналом; отдельные тестовые результаты здесь повторно не получены.

## Исправления

1. В ItomTileService используется TileServiceCompat.startActivityAndCollapse с PendingIntentActivityWrapper из существующей зависимости androidx.core:core-ktx:1.17.0. Проверены сигнатура конструктора и выбор API в официальном source JAR версии 1.17.0. Intent явно адресован MainActivity и сохраняет FLAG_ACTIVITY_NEW_TASK; isMutable=false. Прямого вызова устаревшей перегрузки и подавления lint в приложении нет. Назначение плитки прежнее: открыть UI.
2. При просмотре ProfileScreens найдено приведение LocalContext к Activity, которое проверяет lint Activity Compose. Оно заменено на LocalActivity.current из уже используемой activity-compose:1.11.0. DisposableEffect привязан к Activity; FLAG_SECURE сохраняет прежнее значение и снимается только если выставлен этим экраном. Это обоснованное исправление по исходникам; без полного отчёта нельзя утверждать, что именно оно было второй ошибкой в пользовательском запуске.
3. AGP явно создаёт текстовый и XML-отчёты Lint. Сборщик копирует их в dist, записывает lint-summary.json и выводит все Error/Fatal с координатами в терминал и build.log. Предупреждения сохраняются в полных отчётах. Перед повторным Gradle-запуском удаляются только известные сгенерированные lint-отчёты, чтобы ранний сбой не подхватил старый результат.
4. Ревизия build-fix-4, отдельный ZIP _fix4 и проверка --expect-revision. Предыдущие исправления UTF-8, pair ID, проверки архива и JUnit-отчётов включены.

Lint abortOnError остаётся включён. Проверки не отключены, baseline не добавлен, severity не понижен. APK копируется в dist только после успешного завершения всей команды Gradle. Серверные бинарники, toolchain и APK в source ZIP не включены.

## Проверка этой поставки

22 Python/offline-теста: прежние 18 плюс вывод нескольких блокирующих Lint-ошибок, сохранение UTF-8 и полных отчётов, отсутствие отчёта, повреждённый XML, очистка старых результатов без затрагивания посторонних файлов. Python AST, XML и границы debug/release source sets проверяются прежним checker. Новый ZIP проверяется своим entry point в режиме check.

APK, Kotlin/JUnit и Android Lint build-fix-4 здесь не запускались по договорённости о локальной сборке. Проверка плитки на API 29–33 и 34–36, FLAG_SECURE на устройстве и полный G2 остаются открытыми. Реального TUN/IMAP транспорта этот фикс не добавляет.

## Первичные источники

- [TileServiceCompat](https://developer.android.com/reference/androidx/core/service/quicksettings/TileServiceCompat).
- [PendingIntentActivityWrapper](https://developer.android.com/reference/androidx/core/service/quicksettings/PendingIntentActivityWrapper).
- [Исходники AndroidX Core 1.17.0](https://dl.google.com/dl/android/maven2/androidx/core/core/1.17.0/core-1.17.0-sources.jar).
- [LocalActivity и соответствующее правило Lint](https://developer.android.com/jetpack/androidx/releases/activity#1.10.0).
- [Настройки Lint AGP 8.13](https://developer.android.com/reference/tools/gradle-api/8.13/com/android/build/api/dsl/Lint).
