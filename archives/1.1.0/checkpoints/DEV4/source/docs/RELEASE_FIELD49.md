# FIELD49 / transport-s1-perf7-field28

Исправления по полевым архивам FIELD48 от 2026-10-08. Android versionCode 9008, versionName 0.9.0-dev2-field28. Поставка исходников; APK/AAR/серверы не собирались. Оба конца обновить одной ревизией, рабочие journals не сбрасывать.

## Исправления

1. **Late NACK после NotCommitted.** Definitively unsent Queued item сохраняет первоначальную отправку. NACK больше не переводит его в BeginRepair, который требует Writing/VisibleWait. Исправляет воспроизведённый FLOW_STATE, завершающий логического владельца. Не изменяет authenticated repair для uncertain/published bytes, once-only delivery или physical debt.
2. **Deferred DATA ждёт изменения допуска.** Future-only и omitted batch members возвращаются actor с broadcast token поколения delivery predicate. Пока token не закрыт, actor не создаёт заново Job, payload copy и физическую attempt. Workers/APPEND slots не удерживаются. Ранний ACK перепроверяется под тем же mutex до парковки, cancellation и соседние stream работают независимо. Удалена пауза 1 ms с немедленным повторным расписанием.
3. **Короткий ответ после простоя.** Перед коротким client DATA APPEND primes два deterministic quiet receive stripes. Fresh request прерывает empty-poll delay, сигнал burst coalesces в один slot. PING/OPEN и short DATA response windows используют последний локальный RTT, верхняя граница 2 s. Server reply DATA/repair не создают reciprocal hot state; bulk reserve и отсутствие read hedges сохранены.
4. **Диагностика.** Deferred/stopped/flight-limit теперь имеют разные коды. Native TCP bridge отмечает read/write/eof/half_close в application_copy_end, без socket addresses или error text. Копирование сохраняет bytes, partial-read errors, short-write errors и FIN contract.

## Проверки

Regression test нового NACK сценария падает при изолированной подстановке `engine.go` FIELD48, проходит с FIELD49. Проверены parked cancellation, sibling survival, exact payload/offset after wake, early ACK, reserved-reader scope и copy-stage redaction. Дополнительный сценарий проводит три persistent TCP connection по семь коротких request/response через реальные supervisors/TLS IMAP, включая 2.2 s idle и FIN. Он проверяет целостность протокола, не эмулирует конкретное приложение speed test.

Сводка общего прогона: `reports/field49/validation.json`. Все неудачные development model attempts сохранены. Финальный одиночный synthetic duplex: upload 8.550 / download 8.393 Mbps; последние 20 s 8.716 / 8.192 Mbps; max warm HTTPS RTT 1141 ms, 60 probes, errors 0, unexpected carrier exits 0. Физический shaper независимо калиброван на 20 Mbps full-duplex / 250 ms, shared across all client sockets in each direction. Порог 5 Mbps / 1500 ms не ослаблен.

Это один model run; финальные все три фазы × три повтора, paired A/B, native module tests, Android/TUN/VPS и реальная provider qualification здесь NOT_RUN. Предыдущий model attempt после NACK fix: 8.620/8.048 Mbps, max RTT 1541 ms — FAIL, сохранён. Самую первую попытку остановили FLOW_STATE ошибки до добавления NACK fix — также сохранена. Нельзя выдавать их за прошедшую матрицу или за полевое улучшение.

## Что установлено по обычному спидтесту

В доступном клиентском сегменте все 21 OPEN TCP 8080 получили OPEN_OK. Короткие обмены шли, затем native copy записывал RESET/CLOSED. Начало запуска уже вытеснено из кольцевой записи; source/destination failure stage у FIELD48 не было. Поэтому точная причина отказа интерфейса обычного спидтеста не подтверждена. FIELD49 исправляет воспроизведённые ошибки транспорта и добавляет недостающий stage для следующего полевого прогона. Полный разбор: `FIELD48_LIVE_ANALYSIS_2026-10-08.md`.

## PowerShell

```powershell
python .\ITOM_TRANSPORT_FIELD49_SOURCE.zip all --expect-revision transport-s1-perf7-field28 --accept-android-licenses
```

Только standard core checks без APK/серверов:

```powershell
python .\ITOM_TRANSPORT_FIELD49_SOURCE.zip core --expect-revision transport-s1-perf7-field28
```

Все три model phases × три повтора отдельно, без APK/серверов:

```powershell
python .\ITOM_TRANSPORT_FIELD49_SOURCE.zip model20 --expect-revision transport-s1-perf7-field28 --model-repeats 3
```

Для следующей диагностики сначала запустить обычный спидтест и выгрузить клиентский и серверный trace сразу после ошибки, до длительного второго теста: прежнее начало сессии уже не помещается в ring. Расширять лимит trace ради горячего scheduling loop не нужно: исправить сам loop и сохранить его раздельный код.
