# ITOM FIELD38 — исправление тайм-аута Windows core gate

Ревизия `transport-s1-perf5-field17`, исходники `ITOM_TRANSPORT_FIELD38_SOURCE.zip`.

Пользовательский FIELD37/Windows прогон оборван общим тайм-аутом пакета `tunnel` через 120.339s. Тест `TestMobileBaselineNoopScopeAndCancellation` к этому моменту работал только 3.597s; до него завершённые тесты не содержат assertion FAIL. 120s — бюджет всего test binary пакета, а не каждого теста. Предыдущий полный Linux пакет занял 117.795s, что оставляло слишком мало запаса для Windows.

В stock launcher общий normal package timeout изменён с 120s на 300s; race уже имел 300s. Тайм-аут остаётся конечным. Все тесты запускаются, skipped остаются только прежние optional сценарии; проверка полного набора пакетов/test events, строгие пороги скорости/RTT, per-operation deadlines и cancellation assertions сохранены. Никакие тесты не удалены и performance критерии не ослаблены.

Транспорт, Android, native и все тесты сохранены из FIELD37; в Go изменены только идентификаторы ревизии в команде/трассировке. Сетевые исправления Mail.ru подробно описаны в `docs/FIELD37_RESULTS.md` и `docs/FIELD37_README.md`. Нужно обновить APK и сервер FIELD17 для полевой проверки Android сетевого пути. Rambler в текущую проверку не включён.

Сборка в Windows PowerShell:

```powershell
python .\ITOM_TRANSPORT_FIELD38_SOURCE.zip all --expect-revision transport-s1-perf5-field17 --accept-android-licenses
```

VPS amd64 после загрузки нового бинарника в `/tmp`:

```bash
chmod 755 /tmp/itom-linux-amd64 && sudo /tmp/itom-linux-amd64 install
```

Результаты и границы проверки: `docs/FIELD38_RESULTS.md`. Новый APK/Android lint и реальный Mail.ru не проверялись в этой Linux среде. Это исправление сборочного gate, а не подтверждение восстановления реального соединения или >=75Mbps. При оставшемся отказе экспортировать архивы обеих сторон после обновления APK и сервера.
