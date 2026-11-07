# ITOM: четыре объединённых блока разработки

Текущая ревизия исходников: **transport-s1-perf5-field4**. Результаты приёмки конкретного ZIP публикуются отдельно в ITOM_FIELD4_REPORTS. Внутренние Go-прототипы и причинные FAIL/PASS входят в отчёт; публичный ZIP выпускается на существенный объединённый результат.

| Блок | Этапы исходного плана | Состояние |
|---|---|---|
| 1. Состояние и достоверный стенд | R01–R05, cancellation R15 | field2: обе DB profiles, явная offline migration, независимый payload verify/clock и реальные supervisors; field4 добавляет full load при 50% accounts |
| 2. Прогресс, восстановление и обслуживание | R06–R10, обусловленные R13/R14 | field3: logical owner/rejoin; field4: fair cleanup, usable quota selection, control coalescing, fast service, sustained half-outage qualification |
| 3. Capacity, fairness и provider correctness | R11/R12, оставшиеся R13–R17, Q01–Q03 | В field4 включены причинно необходимые proof/bounds-регрессии; provider quotas, два users и capacity 50→80→100 остаются отдельным крупным блоком |
| 4. Android/VPS и выпуск; governor | Q04–Q06, R18/R19 | Windows all, paired APK/VPS, dirty upgrade, device/soak, затем governor shadow→bounded regulation |

## Уточнённая пользователем доступность

Сохранение соединения требуется при наличии как минимум 50% рабочих аккаунтов: 5 из 10. Сценарий 2 из 10 не блокирует выпуск. Проверяются обе половины и смена доступной половины; те же application sockets, session/generation и exact bytes; target TCP dial не повторяется.

Новый критерий пользователя: ≥30/30 Мбит/с при RTT max ≤600 ms; либо ≥50/50 при max <700 ms; либо ≥100/100 при max <800 ms. Один общий tier должен выполняться для обеих verified directions и завершающего окна. Comparisons используют исходную duration, без округления миллисекунд. Cadence 1 Hz, probe deadline 1 s, zero timeout/missed, useful gap <5 s. Warmup/recovery публикуются отдельно. Все commands и FAIL сохраняются; старые thresholds не переименовываются задним числом.

## Следующие крупные выпуски

- Завершить R11/R12/R15–R17: physical proof при STORE/EXPUNGE/UIDVALIDITY, byte/RAM bounds, account role quotas и provider cancellation/deadlines. Два users: A-only/B-only/A-bulk+B-interactive/оба bulk; изменение RR на cost/DRR только по измеренному unfairness.
- Capacity steps 50/50→80/80→100/100: доказанная физическая capacity, ≥60 s measurement после warmup, повторная серия, fixed windows/debt/per-user RTT. Общий provider cap не выдаётся за такую же useful duplex capacity. Single-stream speed — отдельно.
- Windows all: действительные Kotlin/native/Gradle/lint/APK checks и Linux binary той же ревизии. Device: fresh/dirty upgrade, Wi-Fi/mobile, screen off, DNS/protect(fd), process/network outages. Installer readiness проверяется по transport, затем soak 30–60 min и 6–12 h.
- Governor включается после qualified baseline: shadow, затем bounded bulk regulation с повторной приёмкой. Текущий runtime оставляет его OFF.

Source history: DEVELOPMENT_PLAN_GROUPED_FIELD3.md и PROJECT_HANDOFF_FIELD3.md. Existing 8/128 и fresh 12/192 сохраняются без автоматической миграции; UNKNOWN/Closed остаются physical debt до доказательства. Каждый релиз заканчивается двумя командами: all сборка source ZIP, затем установка бинарника из /tmp.
