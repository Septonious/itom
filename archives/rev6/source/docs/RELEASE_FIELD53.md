# ITOM FIELD53 experimental / transport-s1-perf7-field32

Дата: 9 октября 2026. Android versionCode 9012. Отправная точка: exact FIELD52 source (`transport-s1-perf7-field31`). FIELD51 остаётся откатом стабильности, FIELD52 остаётся последней реально измеренной версией.

## Полевая исходная точка, не обещание скорости FIELD53

Speedtest FIELD52 на Android: **22.5 Mbps down / 25.9 Mbps up**. Idle ping 384ms; download loaded ping 371ms; upload loaded ping 379ms; jitter 60ms. Цель следующего теста 40/40 Mbps без ощутимого ухудшения RTT.

Проверен пользовательский `ITOM_diagnostics_full(20261008-223013).zip`: три кольцевых trace за 22:26:23–22:29:45 UTC, 112288 events, клиентская сторона. В health у 10 аккаунтов `ProbeAttempts/ProbeAccepted/ProbeRejected = 0`; следовательно, реальное улучшение нельзя приписывать экспериментальной логике capacityProbe FIELD52. Обычный delivery window достиг 1361920 bytes (~1330KiB), ранее опускался до 768KiB во время возрастного давления. Пиковая контроллерная полезная `RateMbps` из health ~24.1 Mbps у ряда аккаунтов (это session-wide snapshot; значения у аккаунтов не суммировать). Последние fast ACK во время пиковой передачи ~337–343ms; в другие моменты заметно выше. В пакете трассы встречаются короткие `DELIVERY_MEMBER_DEFERRED`, normal flow control, а также `IMAP_APPEND_OUTCOME_UNKNOWN=2` и пользовательское завершение VPN; не объявлять протокол полностью free-of-errors по частичной клиентской трассе. `diagnostics.json` показывает `phase=OFF` и не является live-снимком при нагрузке. Серверная трасса для той же попытки отсутствует.

## Изменения относительно FIELD52

- `fastRamp`: дополнительный ускоренный путь внутри существующего deliveryController. При ongoing bulk demand, не менее 4 свежих bulk residence samples, useful ACK >=16Mbps, свежем (<3s) последнем short ACK <=525ms и хорошей bulk residence (< target -150ms) логическое окно растёт на 192KiB/decision epoch, вместо 64KiB FIELD52. На медленной линии и при отсутствии данных о short ACK остаётся прежняя 64KiB политика. Физические IMAP permits не меняются.
- `fastRamp` привязан к последнему безопасному логическому окну при первом ускорении. Новое подтверждение короткого DATA с задержкой >110ms относительно начала ускорения, short ACK >600ms или находящийся в полёте short DATA старше600ms вызывает возврат окна к anchor и 18s cooldown. Существующее аварийное снижение окна по bulk residence/fast pressure продолжает работать, независимо от fastRamp.
- Увеличенное окно никогда не отменяет долгов по журналам/неподтверждённым DATA; short reserve остаётся128KiB, min768KiB/max3MiB. Поля `FastRampIncreases/FastRampRollbacks/FastRampActive` в governor trace покажут, выполнился ли механизм. FIELD52 `capacityProbe` на plateau оставлен с прежними условиями. Wire, durable accounting, FTP/IMAP semantics, Android app logic и число сокетов не менялись.

## Состояние валидации

Изолированный Go1.23 контроллер с контрактными stubs (NOT production core build): 11 unit+integration tests PASS, в том числе `-race`. Python launcher/offline package checks и exact ZIP SHA256/manifest проверяются при packaging. Полные Go1.27.1 suite, model20/model130, core/native builds, Kotlin/Gradle APK, реальная сеть и VPS/device FIELD53: **NOT RUN** в этой среде из-за отсутствия pinned Go toolchain и зависимостей. **40Mbps и отсутствие RTT-регрессии пока НЕ подтверждены.** Это экспериментальный источник для квалификации, а не стабильный production release.

Строгий gate для 20Mbps physical-full-duplex model: у каждой активной direction и tail >=5Mbps, максимальный HTTPS RTT <=1500ms, ошибок0. Не расширять пороги из-за цели 40Mbps. Для реальных 130Mbps A/B: FIELD52 baseline vs FIELD53 candidate, idle/download/upload ping, download/upload throughput, jitter, 10 account health, short ACK age и FastRamp counters. При росте loaded ping >=75–100ms либо многочисленных сбоях вернуть FIELD52/ FIELD51.

## Сборка Windows

```powershell
python .\ITOM_TRANSPORT_FIELD53_SOURCE.zip all --expect-revision transport-s1-perf7-field32 --accept-android-licenses
```

Обновить одновременно Android и VPS с той же ревизией; не удалять журналы, папки и настройки. На VPS после копирования файла: `chmod 755 /tmp/itom-linux-amd64 && sudo /tmp/itom-linux-amd64 install`.
