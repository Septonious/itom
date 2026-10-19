# Native UI 1 · исправление 1

Дата: 2026-09-24. Версия: 0.4.0-dev2, versionCode 4003, ревизия `native-ui-1-fix1`.

## Основание

Проверена точная исходная поставка `ITOM_PHASE2_SOURCE_0.4.0-dev2_ui1.zip` с SHA-256 `9791a1a49648b6397d2fc5d4c53e6f5c61064328ed350472e7c419204015d466`; внутренний SOURCE_MANIFEST также совпадает. Журнал пользователя показывает успешный `:domain:test`, затем ошибки `:app:compileDebugKotlin` и `:app:compileReleaseKotlin` в одной строке HomeScreens.kt. Lint не дошёл до отчёта. Предупреждения SDK XML v3/v4 и strip native library не являются причиной этого падения.

## Исправления

В `ConnectionHero` внутри `BoxWithConstraints` вложенный `Row` скрывал внешний неявный DSL receiver. Чтение `maxHeight/maxWidth` в аргументе `Modifier.size` поэтому не компилировалось. Теперь оба размера сохраняются в локальные значения до входа во вложенные layout-контейнеры; формулы размеров используют эти значения. Та же явная граница применена ко второму `BoxWithConstraints` в Home. Поиск по Kotlin-исходникам не обнаружил других неявных обращений к этим ограничениям из вложенных layout scopes. Формулы компоновки, отсутствие прокрутки и работа кнопки сохранены.

В манифесте приложению был назначен `@drawable/ic_itom` — прозрачный drawable с белым конвертом для small notification icon. Launcher и системный список приложений получали именно его. Приложению назначены отдельные `@mipmap/ic_launcher` и `@mipmap/ic_launcher_round`: тёмный градиент, конверт, голубая орбита с мягким ореолом, зелёный спутник. Добавлены adaptive layers для API 26+, отдельный monochrome layer для тематических иконок API 33+ и векторные fallback-варианты. Основной знак помещается в центральную безопасную область 66 dp слоя 108 dp. У monochrome нет непрозрачного прямоугольного фона.

Уведомление и плитка по-прежнему используют монохромный `ic_itom`. Каталог приложений уже загружает полный drawable PackageManager и выводит его через Compose `Image` без tint; этот код не изменён. Увеличен versionCode до 4003; package ID, хранилище и подпись сборки не менялись.

## Проверки и ограничения

- 30 существующих Python/offline-тестов прошли с `PYTHONUTF8=0` и EncodingWarning как ошибкой; разобраны 4 Python-файла и 13 XML, проверены границы source sets.
- Отдельно проверены ссылки манифеста, adaptive layers, fallback-ресурсов, уведомления и плитки. Цветные и тематический векторы отрисованы из XML через промежуточный SVG для проверки формы, включая круг/скруглённую маску и размер 48 px. Это проверка ресурсов, не Android rendering или screenshot приложения.
- Итоговый source ZIP повторно проверяется командой `check` с новой распаковкой и контролем хешей. Исходники backend, маршрутизации, хранилища, сборщика, тестов и UI вне Home не изменены относительно native-ui-1.
- APK, Android compilation/Lint, текущие Kotlin/JUnit-тесты и device tests здесь не запускались по договорённости с пользователем. Локальная команда по-прежнему требует успешных domain tests, Lint, release Kotlin compilation и debug assembly перед экспортом APK. Проверки не отключались.

Новый архив: `ITOM_PHASE2_SOURCE_0.4.0-dev2_ui1_fix1.zip`. Команда:

```powershell
python .\ITOM_PHASE2_SOURCE_0.4.0-dev2_ui1_fix1.zip android --expect-revision native-ui-1-fix1 --accept-android-licenses
```

## Документация платформы

- Kotlin DSL scopes: https://kotlinlang.org/docs/type-safe-builders.html#scope-control-dslmarker
- Adaptive icon layers, safe zone и monochrome: https://developer.android.com/develop/ui/compose/system/icon_design_adaptive
- VectorDrawable и gradient colors: https://developer.android.com/reference/android/graphics/drawable/VectorDrawable

Более ранние документы аудита остаются историей своих ревизий; результаты текущей поставки записаны в `reports/SOURCE_CHECKS.json`.
