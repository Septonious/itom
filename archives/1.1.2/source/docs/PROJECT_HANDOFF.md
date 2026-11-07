# Актуальное решение — 7 октября 2026

Пользователь утвердил полную переработку транспортной оркестрации и Go-стенда по `TRANSPORT_REDESIGN_ANALYSIS_2026-10-07.md`. Рабочий контракт и ограничения закреплены в `TRANSPORT_REDESIGN_DECISIONS.md`; они заменяют исторические performance targets ниже. Во время разработки запускаются только точечные тесты, перед упаковкой кандидата — общий стандартный прогон.

Текущий код: FIELD47 / `transport-s1-perf6-field26`, Android versionCode 9006. Реализован первый этап: socket lifecycle/first cause, not-started vs unknown APPEND, account dial pacing, MIME-byte deficit scheduling с ограниченным short DATA приоритетом, единственный receive flight без cleaner FETCH, ограниченный terminal budget и demand-driven control. Полный стандартный Go/fork/vet/server gate и Android/Kotlin/lint/native проверки прошли. Итог отдельной модели 20 Мбит/с — `reports/field47/validation.json`; это не полевая квалификация. Сборочный localhost критерий 27 Мбит/с убран, точный пользовательский 5 Мбит/с / 1500 мс не ослаблен.

Следующий этап: delivery feedback/governor, receive recovery по прогрессу, калиброванная provider/fault модель и paired Android/VPS/provider A/B. Существующие journal, quota/proof и wire contract сохранены. Не повторять полный Go sweep во время разработки без причины; перед новым исходным кандидатом выполнить общий стандартный gate. Детали реализованного и оставшегося — `RELEASE_FIELD47.md`. Исторические handoff ниже не являются текущим статусом или текущими порогами.

# FIELD32 update — restored RTT/bandwidth governor, not a qualified release

Current revision transport-s1-perf5-field10. User explicitly requested restoring the gate51 experimental latency-first controller on 2026-10-05; the historical OFF instruction is superseded. Original gate51 source/tests remain unchanged under docs/experimental/*.disabled. Current runtime uses an operational 900ms Echo target with 100ms headroom under the strict <1000ms application RTT acceptance; baseline+400ms is preserved as historical design, not current acceptance. Real-provider and Android validation remain required. See FIELD32_FINDINGS.md and the exact report/evidence before release.

# Исправление сборки: SOURCE27

SOURCE27 исправляет сборочный smoke gate четырёх аккаунтов после Windows FAIL SOURCE26:28.412999Mbps/660.1989ms. Исполнение транспортного кода не меняется. TestLoadedApplicationRTT и UploadWithTrace продолжают проверять точные байты, ошибки живых lanes и directional throughput; build gate:30Mbps с10% допуском(27), RTT<=1000ms без расширения cap. Старые30/600,50/700,100/800 tiers остаются диагностикой. Baseline-aligned правила supervised/mobile production harness не изменены. Сборочный gate не подтверждает125+ приёмку. Windows execution и Android APK здесь не выполняются.

## Состояние предыдущего сборочного среза

# Завершение этапа: SOURCE27

По указанию пользователя от 2026-10-04 текущий этап завершён сборочным срезом SOURCE27. Транспортная логика точно совпадает с SOURCE24: ухудшившие показатели изменения SOURCE25 (poll2ms, ceiling140) отменены; незавершённый control-admission прототип не включён. Обязательный Go/core/race/build gate выполняется для этого ZIP. Нагрузочная приёмка125+ и полное парное сравнение не завершены и не объявляются PASS. Потери пакетов и200Mbps отложены. Строгие RTT/скоростные критерии не изменены; это сборочный выпуск, а не подтверждение полного performance gate. Результаты и SHA256 публикуются отдельно в ITOM_SOURCE27_BUILD_RESULTS.zip.

## Отложенная нагрузочная квалификация и история

# SOURCE27: текущий этап нормальной сети

Критерии и команды — README.md и ACCEPTANCE_FIELD5.md. Выпуск разрешён только после полного точного Go/core/race/fork/vet/native/build gate, fixed normal-network matrix и >=3paired field4 repeats/profile без подтверждённого ухудшения>10%. Наличие архива само по себе не означает приёмку. Packet-loss qualification отложена2026-10-04;200Mbps отложено.

Runtime сохраняет bounded journals, one-shot APPEND, Unknown physical debt, checked scope/crypto и quota refunds только после actual deletion proof. Локальный проверяемый IMAP fork использует advertisedLITERAL+ и требует полной requested UID/BODY/SIZE формы; partial FLAGS updates не поглощают ожидаемый BODY. Если requested UID после tagged-success FETCH остался только update, boundedUIDSEARCH отличает удалённое письмо от существующего неполного; ошибки/избыточный scope не скрываются.

Streaming полные, scope-checked BODY передаются до конца FETCH; cursor/inventory обязаны дождаться успешного tag. Все readers продолжают bounded poll при активных sibling streams, empty active pause5ms; quiet IDLE и wake/cancellation сохранены, FETCH page16/hot4. Fast grant selection и физический slot reserve выполняются без возможности bulk overtaking; local fast admission имеет отдельный wake. Cleanup использует FIFO handoff и оставляет fast carriers.

S2C bulk pacing10–150Mbps учитывает authenticated BODY receipts и выполняется после quota/journal admission перед physical admission. Waiting bulk не держит physical slot; cancelPrepared освобождает только неиспользованный grant. Receipt не является GC proof. Physical quotas12MiB total для fresh DATA,8MiB existing; local/remote shares и replacement headroom не увеличивают общий предел. В FIELD32 общий governor восстановлен по прямому указанию пользователя; download pacer остаётся активным.

Текущая квалификация:10accounts,1user,4bulk streams в каждом направлении;0%loss;60s measurement+10s warmup; baseline100/200ms cap600 с3seeds и парным field4, узкие50/150Mbps иbaseline300/cap200; обе половины5/10 и5→10 recovery. Не моделируются>50% outages. Numeric throughput проверяется безrace; concurrency/integrity — сrace. Самопроизвольный rejoin на здоровом carrier блокирует case; intentional carrier epochs могут измениться, logical/session/app sockets сохраняются.

Source23 и24 rejected logs сохраняются: source23max754ms иdiagnostic timeout; source24mean137.096/140.342,max649.433ms, одна проба на1.254ms выше своего648.178ms limit. SOURCE25 отклонён после ухудшения RTT и скорости; его tuning отменён в SOURCE27.

Windows actual execution, Android/device, настоящий provider и VPS soak остаются отдельной квалификацией. Windows cross-compile не PASS Windows tests. История — HISTORY_SOURCE24_PROJECT_HANDOFF.md и CONTINUATION_SOURCE*.md.
