# ITOM FIELD39 FIX2

Исправление воспроизводимости Go-сценария provider_cap_recovers и диагностики отказов сборки. Ревизия транспорта: transport-s1-perf5-field18.

Тест занимает реальные IMAP-сессии после активации линий, подтверждает отказ DATA во время HTTP-нагрузки и восстановление ёмкости на исходных линиях. HTTP/2 upload/download, проверка байтов, короткие HTTP/1.1 запросы под нагрузкой и контроль отсутствия переподключений сохранены. Таймауты, production транспорт и правила Go gate не ослаблены.

FIX1 для Kotlin Invitation включён. Production core/native/Android соответствует FIELD39. Эта сборочная правка не подтверждает реальную стабильность провайдеров или скорость75Мбит/с.

Подробные результаты: docs/FIELD39_FIX2_RESULTS.md. Предыдущие этапы: docs/FIELD39_FIX1_RESULTS.md и docs/FIELD39_ORIGINAL_README.md.

```powershell
python .\ITOM_TRANSPORT_FIELD39_FIX2_SOURCE.zip all --expect-revision transport-s1-perf5-field18 --accept-android-licenses
```

Для VPSamd64 после копирования бинарника в/tmp:

```bash
chmod 755 /tmp/itom-linux-amd64 && sudo /tmp/itom-linux-amd64 install
```
