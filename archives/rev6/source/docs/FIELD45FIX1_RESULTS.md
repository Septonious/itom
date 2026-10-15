# FIELD45 FIX1 — исправление Windows 8.3 paths

7 октября 2026. Ревизия `transport-s1-perf5-field24`, Android versionCode 9004.

Причина остановки сборки: тест сравнивал строки путей. Windows TemporaryDirectory мог вернуть `C:/Users/SEPTON~1/...`, а сборщик через Path.resolve вернул тот же каталог как `C:/Users/Septonious/...`. Это ошибка теста, а не установки SDK. Исправлено на `Path.samefile`, проверяющий идентичность существующих каталогов.

От FIELD45 изменены только `tools/test_core_tools.py`, README и имя архива в source-info.json. Исходники Go, native, Android и самого launcher/SDK-сборщика побайтно совпадают с FIELD45. Ревизия и Android versionCode не повышены, поскольку работа приложения не менялась.

Перед упаковкой на Linux:

- Точечные 4 SDK-теста: PASS. Тот же тест дополнительно выполнен через реальный filesystem alias, отличающийся от канонического пути: PASS.
- Полный Python-прогон: 34 + 36 = 70 тестов PASS, структура исходников PASS.
- Полный Go-прогон: 19 пакетов PASS, 477 успешных test events, 4 opt-in теста пропущены.
- Все native-тесты с race detector: PASS.
- Полный стандартный Android gate: PASS; 6 Gradle-задач исполнены, 57 up-to-date. Неизменённые Kotlin-результаты: 60 тестов PASS. Lint и сборка APK успешны.
- TestLoadedApplicationRTT: 36.566 Мбит/с в каждом направлении, максимум RTT 335 мс. Порог 27 Мбит/с / 1000 мс сохранён и пройден.

Windows здесь не запускался. Исправление проверено по фактическому Windows traceback пользователя и тесту с альтернативным именем реального каталога на Linux.

Мобильная приёмка **НЕ пройдена**: код транспорта прежний. Результат модели FIELD45 — скорость выше 5 Мбит/с во всех активных направлениях и последней трети, но RTT максимум 2875 мс выше предела 1500 мс. Модель повторно не запускалась при этой правке одного Python-теста. Полные исходные измерения находятся в FIELD45_RESULTS.md/json; журналы FIX1 — в field45fix1-checks/.

Команда:

```powershell
python .\ITOM_TRANSPORT_FIELD45FIX1_SOURCE.zip all --expect-revision transport-s1-perf5-field24 --accept-android-licenses
```
