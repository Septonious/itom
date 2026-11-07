# FIELD51 / transport-s1-perf7-field30

Android versionCode9010, versionName0.9.0-dev2-field30. Source-only release, оба конца обновить одной ревизией. APK/AAR/deployable сервер не собирались.

## Изменения транспорта

1. Delivery controller сохраняет bulk admission demand в течение1s decision epoch. Раньше периодический maintainer мог увидеть уже пустой список waiters после парковки DATA jobs и потерять demand, сбросив хорошие ACK samples без роста окна. Новый virtual-clock regression падает на FIELD50. Demand потребляется однократно, истекает через1s; short/repair waiters не создают bulk demand. Stall shrink, accounting authenticated ACK, initial1MiB, окно768KiB..3MiB и fast reserve128KiB сохранены.
2. Fast DATA имеет отдельный feedback: unacknowledged short range старше1000ms либо недавно подтверждённый range с таким residence запрещает рост bulk и уменьшает его окно на1/8 за decision epoch. После исчезновения pressure действует2s hold, затем нормальное восстановление. Charged bytes не возвращаются без ACK/terminal; пределы768KiB..3MiB/128KiB не изменены. Late fast ACK не уменьшает idle budget без bulk demand. Это ограничение роста, а не обещание appRTT<=1500 для любой130Mbps сети. Диагностика: LastFastACKAgeMS, FastPressureRemainingMS, FastPressureDecisions. 1000ms ACK feedback оставляет запас для второй DATA leg приложения; это не изменение1500ms app gate. Lower floor768KiB позволяет разгрузить очередь ниже стартового1MiB, сохраняя charged debt. Не использовать priority PING вместо application DATA feedback.
3. Reader reset: свежий response hint увеличивает отдельный generation; следующий receive turn сбрасывает предыдущую empty-poll streak. Обычные bulk/GC wakes её не сбрасывают. Установившаяся hot cadence10/20/40/80ms и active/idle audits сохранены. TLS/socket не перестраивается; новые hints всё ещё coalesce в one-slot wake. Новая functional regression воспроизводит140ms между FETCH starts при100ms command cost на старом reader и проверяет раннюю повторную проверку нового.
4. Core gate записывает core_runtime_sha256 проверенной копии runtime и отвергает её расхождение с исходниками до/после проверки. model20/model130 используют тот же fingerprint. Новый launcher regression проверяет, что изменённая тестовая копия не может получить PASS. Это fingerprint first-party Go runtime, не всего native/Android; fork проверяется отдельным pin.
5. Short DATA сохраняет приоритет quiet reader, пока у него есть свободный effective DATA slot. Раньше один занятый слот вместе с независимым control APPEND снимал reserve bonus и направлял следующую короткую запись на bulk BODY reader. Новый functional regression проходит selection, reservation/release, fallback при заполнении обоих slots и уменьшении socket capacity; FIELD50 выбирает a03 вместо свободного reserved reader. Предел physical slots и journal capacity не увеличен.
6. RTT диапазон fixture согласован с130 Go harness:0..1000ms. CLI profile остаётся250ms,20 profile100..300ms и gate5Mbps/1500ms не менялись.

Эксперимент FETCH4→8→16UID исключён из runtime: первоначальное130 сравнение показало улучшение только download и ухудшение upload/duplex. Это не доказательство причинности или устойчивой регрессии, но положительный эффект не установлен. Patch и все исходные результаты сохранены в development_attempts. Production tail остаётся4UID. Окончательная версия повторно измеряется отдельно. FIELD51 поставляется как тестовая версия: исправление QoS selection доказано функционально, рост скорости на130Mbps не установлен. Результаты ниже следует читать до обновления.

OPEN allocation/cancellation, nil Stream на ошибке, event-based DATA wake и late-NACK исправления FIELD49/FIELD50 сохранены. Отказ обычного Speedtest и idle RTT реального Android остаются непроверенными: новые полевые traces после FIELD50 отсутствуют.

## Проверки и результаты

Окончательные результаты: reports/field51/validation.json, raw event streams и таблица ниже. Во время разработки использованы точечные race tests; полный standard core gate выполняется перед упаковкой.

130Mbps, одна60s фаза каждой версии:

| Фаза | FIELD50 down/up Mbps | FIELD51 down/up Mbps | Max RTT50/51 ms |
|---|---:|---:|---:|
| download | 14.527/— | 9.096/— | 1110/1048 |
| upload | —/19.905 | —/8.935 | 1088/1287 |
| duplex | 16.244/10.783 | 8.323/5.225 | 1248/1170 |

20Mbps final gate:

| Фаза | Down/up Mbps | Tail down/up Mbps | Max RTT ms |
|---|---:|---:|---:|
| download | 7.397/— | 6.855/— | 1325 |
| upload | —/8.498 | —/8.022 | 1115 |
| duplex | 6.903/6.890 | 7.078/7.039 | 1417 |

Все20 criteria PASS; ошибки0, carrier exits0.130 idle HTTP final:553,539,825,566ms. Снижение реального idle и исправление обычного Speedtest не подтверждены.

Full core:19packages, 522PASS events (including subtests/seeds), bundled IMAP tests и vet PASS. Targeted delivery/reader race33PASS. Launcher34/core-tools41PASS; Kotlin60cases present NOT_EXECUTED.


Сравнение выполняется последовательно FIELD50→FIELD51, между ними были отдельные development attempts; по одному repeat, download/upload/duplex по60s с10s warmup.130Mbps calibrated full-duplex, synthetic pathRTT250ms, assumed provider service,16 bulk streams на направление,24 background HTTPS workers,6 warm reused TLS probes. Это measurement-only, не квалификация реальной сети130Mbps. Один последовательный A/B не отделяет изменения runtime от естественного разброса стенда.

Отдельный20Mbps run имеет три фазы и проверяет каждое активное useful direction и tail>=5Mbps, max applicationRTT<=1500ms, errors0. Допуски не добавлены. Данные throughput считаются только внутри фиксированного60s окна.

NOT_RUN: реальный Android/gVisor/TUN/VPS/provider, ordinary Speedtest, native Go compilation/tests (в FIELD50 blocked missing pinned dependencies), Kotlin compilation/tests/lint, APK/AAR/deployable server assembly, full-race/timed-fuzz, repeated model matrix.

## PowerShell

Сборка локально:

```powershell
python .\ITOM_TRANSPORT_FIELD51_SOURCE.zip all --expect-revision transport-s1-perf7-field30 --accept-android-licenses
```

Только полный core gate:

```powershell
python .\ITOM_TRANSPORT_FIELD51_SOURCE.zip core --expect-revision transport-s1-perf7-field30
```

Все три фазы130Mbps, один repeat:

```powershell
python .\ITOM_TRANSPORT_FIELD51_SOURCE.zip model130 --expect-revision transport-s1-perf7-field30 --model-repeats 1
```

Gate20Mbps, три фазы, один repeat:

```powershell
python .\ITOM_TRANSPORT_FIELD51_SOURCE.zip model20 --expect-revision transport-s1-perf7-field30 --model-repeats 1
```

При повторном отказе экспортировать trace клиента и сервера сразу после первого запуска Speedtest, до другого длинного теста: прежние ring buffers утратили начало отказа. Рабочие journals не сбрасывать.
