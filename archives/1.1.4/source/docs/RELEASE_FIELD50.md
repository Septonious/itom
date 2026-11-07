# FIELD50 / transport-s1-perf7-field29

Android versionCode 9009, versionName 0.9.0-dev2-field29. Исходники для локальной сборки; APK/AAR и deployable серверные бинарники не собирались. Клиент и сервер обновить одной ревизией, рабочие журналы не сбрасывать.

## Изменения

- OPEN reserve учитывает ожидающие ответы и активные routes, а не только уже принятые TCP. Выбор и резервирование атомарны под Client.mu. Полный owner с лимитом 64 не вытесняет свободного соседа. Если все здоровые owners заполнены, ReasonCapacity относится только к новому сокету. Terminal retirement, carrier rejoin и owner stop не создают или не теряют capacity.
- Caller cancellation после передачи OPEN actor больше не tombstone-ит маршрут преждевременно. Actor принимает поздний OPEN_OK и закрывает непринятый app socket штатным terminal path. OPEN_ERROR и заранее отменённый request освобождают reservation. Late reply не оставляет TCP allocation на сервере. На ошибке Client.Open возвращает настоящий nil Stream.
- Delivery adaptation получает bounded floor минимального DATA ACK пути из original ranges при малом logical flight. Lower quartile кольца8, minimum4 observations; floor capped900ms, target800..1200ms. Repair samples исключаются, stalled full-window traffic не поднимает baseline. Окно1..3MiB, fast reserve128KiB, authenticated ACK и доказательства освобождения физического долга сохранены. Это не изменение приёмочного предела RTT1500ms.
- Новый `model130` отдельно измеряет calibrated130Mbps / synthetic250ms access, assumed provider costs, 16 bulk streams на направление, 24 background HTTPS workers, 6 warm probe connections и clean idle HTTP observations. Measurement-only: его завершение без ошибок не квалифицирует скорость или задержку. Результаты130 не могут завершить gate20. `model20` сохраняет точные5Mbps/1500ms.
- Новый функциональный сценарий держит 96 TCP одновременно до первого обмена, проверяет 4 request/response каждого, большие records, FIN и следующий OPEN после burst. Сохранены исправления FIELD49: late-NACK FLOW_STATE, event-based deferred DATA и fresh-request receive wake.

## Что доказано

Два owner-reservation regression tests и сценарий позднего ответа на отменённый OPEN падают с runtime FIELD49, проходят с FIELD50. В последнем старый runtime сохраняет client0/server1 route после cancellation; новый освобождает оба конца без carrier rebuild. Сам по себе сценарий96 streams проходит уже на FIELD49 — гипотеза «любое множество TCP ломает транспорт» не подтверждается.

Первый полный Go sweep прошёл все19 пакетов, но сводка launcher отвергла новый optional130 SKIP. Добавлен только именованный opt-in тест в whitelist, проверено, что его FAIL остаётся ошибкой; standard gate затем повторён успешно.

Стандартный полный core gate, bundled IMAP tests, Go vet, targeted race и две отдельные модели записаны в `reports/field50/validation.json` вместе с исходными event streams. До упаковки проверяется, что core runtime hash совпадает с финальными моделями. Development measurements не удаляются и не выдаются за финальную приёмку или paired A/B.

Финальные60s duplex measurements той же core runtime ревизии:20Mbps upload8.856/download8.424Mbps, tail8.808/8.428Mbps, max RTT1360ms,60 probes,errors0; критерий5/1500 PASS. Отдельный130Mbps: upload11.189/download11.954Mbps, tail11.259/11.718Mbps, max RTT1142ms,60 probes,errors0. Неожиданные carrier exits0 в обоих. Idle HTTP130 после1.1s паузы:627,715,914,771ms при synthetic accessRTT250ms. Они не подтверждают улучшение реального idle. Каждая модель выполнена как один duplex run; полная3phase×3repeat матрица и pairedAB здесь NOT_RUN.

Уточнение130Mbps меняет оценку полевых результатов. Первоначальная модель130 дала up10.844/down10.757Mbps; ранняя поправка ACK-floor ещё не дала убедительного ускорения обеих сторон. Поэтому релиз не обещает восстановление130Mbps или снижение реального idle RTT. Нативный gVisor/TUN test пытались запустить без загрузки зависимостей: setup заблокирован отсутствием pinned tun2socks/gVisor/x-net в cache, выполнение NOT_RUN. Android tests/compilation/lint и actual speed-test application здесь NOT_RUN. APK/AAR/server build не выполнялся.

Полный разбор: `FIELD50_130M_CONCURRENCY_ANALYSIS.md`. Точная причина отказа приложения ещё не установлена: сохранённый trace начинается позднее первоначального запуска; все21 видимое OPEN TCP8080 получили OPEN_OK. Часы Android/VPS не синхронны, сырые one-way интервалы не использовать для вывода о задержке.

## PowerShell

Локальная сборка пары:

```powershell
python .\ITOM_TRANSPORT_FIELD50_SOURCE.zip all --expect-revision transport-s1-perf7-field29 --accept-android-licenses
```

Только core проверки:

```powershell
python .\ITOM_TRANSPORT_FIELD50_SOURCE.zip core --expect-revision transport-s1-perf7-field29
```

Все три фазы отдельного130Mbps измерения, один повтор, без APK/серверных бинарников:

```powershell
python .\ITOM_TRANSPORT_FIELD50_SOURCE.zip model130 --expect-revision transport-s1-perf7-field29 --model-repeats 1
```

Приёмочная20Mbps модель, три фазы × три повтора:

```powershell
python .\ITOM_TRANSPORT_FIELD50_SOURCE.zip model20 --expect-revision transport-s1-perf7-field29 --model-repeats 3
```

Если обычный спидтест снова завершается ошибкой, экспортировать Android и серверный trace сразу после неё, до второго длительного теста. Указать число активных TCP из UI и факт использования VPN/SOCKS, не публикуя секреты. Показания model130 — отдельное синтетическое измерение; сравнивать их с полевыми можно только с учётом того, что RTT/service costs реального маршрута не калиброваны.
