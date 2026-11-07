# FIELD47: transport rewrite baseline

Ревизия `transport-s1-perf6-field26`, Android `0.9.0-dev2-field26`, versionCode 9006. Экспериментальный исходный кандидат; полевая Android/VPS/provider квалификация пока не проведена. Принимать как исправление всех причин регрессии преждевременно.

## Реализовано

- Account-level сериализация CONNECT/LOGIN и общий cooldown для ремонта DATA, receive и maintenance; на сервере он разделяется пользователями аккаунта, включая начальное подключение. Аккаунты не блокируют друг друга.
- Вместо справедливости по количеству писем — deficit scheduling по MIME-байтам. Отдельная bounded очередь short DATA, ограниченная серия приоритетного обслуживания и прежний общий byte/outstanding предел. Класс передаётся от logical writer, а не угадывается по размеру письма.
- Наблюдённый decoder EOF исключает сокет до APPEND. Отказ до публикации имеет тип `IMAP_APPEND_NOT_STARTED`; только такой доказанный отказ разрешает замену сокета и попытку начать команду. После возможной публикации сохраняется `IMAP_APPEND_OUTCOME_UNKNOWN`, без автоматического resend и без refund.
- Безопасная телеметрия socket ID/generation/role/worker ID, первого закрытия, idle age, actual TLS transport read/write bytes. В snapshots учитываются общие server DATA sockets. Target dial имеет отдельные стадии slot/DNS/connect и длительности, без hostname/IP/credentials.
- Единственный receive command/cursor. Удалён speculative FETCH через cleaner; отмена возвращается после завершения команды. Empty polling имеет backoff и больше не вращается постоянно с паузой 2 мс.
- Idle stream STATUS прекращается. Потерянные начальное/последующее окна восстанавливаются явным QUERY при необходимости кредита. RESET имеет один owner и ограниченный бюджет отправки; поздний ACK сохраняет возможность завершения, физический долг остаётся до доказанной GC.
- Критерий CPU-зависимого localhost smoke 27 Мбит/с больше не останавливает сборку. Полезные байты, integrity, lifecycle и quota проверки сохранены. Модельный критерий 20 Мбит/с отдельный и точный: >=5 Мбит/с для каждого активного направления и последней трети, RTT <=1500 мс, отсутствие ошибочных проб.
- Нагрузочное окно не зависит от медленных HTTP probes. Учитываются только проверенные прикладные байты в фиксированном интервале; warmup/teardown/late delivery исключены. Поздние пробы присоединяются к RTT/error результату. FIELD46 replay заменяет FIELD12, client/server samples разделены, DATA определяется по ITD3/ITQ1 envelope.

## Что ещё требует следующего этапа

Сохранены существующие wire format, journal/proof contract и delivery governor/window. Полная замена обратной связи по bulk residence/delivery, согласованный отдельный control folder, калиброванный stateful provider с virtual clock и реальные paired A/B остаются следующими этапами. Wrapper-duration replay — явно некалиброванная стресс-модель; наложение её длительностей на физический access RTT не выдаётся за модель реального провайдера. Набор конкретных TLS fault tests уже различает idle EOF и accepted APPEND с потерянным tagged reply.

## Локальная сборка

```powershell
python .\ITOM_TRANSPORT_FIELD47_SOURCE.zip all --expect-revision transport-s1-perf6-field26 --accept-android-licenses
```

В checkout, с Go 1.27.1 из build cache, отдельный полный модельный прогон:

```powershell
$env:ITOM_FIELD_LOAD="1"
$env:ITOM_FIELD_MOBILE="1"
$env:ITOM_FIELD_SECONDS="60"
$env:ITOM_FIELD_STREAMS="4"
$env:ITOM_FIELD_WARMUP_SECONDS="10"
$env:ITOM_FIELD_LATENCY_PROFILE="median"
go test ./tunnel -run '^TestFieldHTTPSColdStartAndDuplex130$' -count=1 -timeout=480s -v
```

Команда запускается из `core`; историческое имя test сохранено для совместимости. Test выводит download/upload/duplex отдельно. Этот стресс-прогон может FAIL; такой результат не скрывается успешной функциональной сборкой. Реальный device/provider результат остаётся отдельным `NOT_RUN` до полевых измерений.

Профиль `median` — историческое имя для заданных постоянных service delays (SELECT 20 мс, APPEND 60 мс, FETCH 16 мс, STORE/EXPUNGE 55 мс), а не калибровка медиан реального провайдера. Профиль `recorded` использует FIELD46 wrapper samples и остаётся некалиброванным stress replay. Оба добавляют отдельный physical access shaping; численный результат одного нельзя переносить на другой.

Полезные байты считаются в фиксированном окне 60 секунд. Нагрузка продолжается до завершения запущенных внутри окна RTT-проб; все поздние пробы учитываются полностью, поздние байты не учитываются. Остановка bulk и создаваемый ею RESET/FIN burst следуют после join. Первый прогон с немедленной остановкой bulk дал duplex FAIL (1630 мс у последней пробы); результат сохранён в `reports/field47/model20-first.txt`, надёжность teardown не объявлена квалифицированной.

Результаты подготовки этого кандидата перечислены в `reports/field47/validation.json`. Ошибки первого общего прогона и повторная проверка после исправлений отмечаются в отчёте; успех localhost не называется подтверждённой скоростью на телефоне.

## Итог проверки кандидата

Стандартные проверки: 70 Python, полный Go sweep (19 packages, 500 успешных test events, 4 optional skips), IMAP fork, vet, сборка серверов amd64/arm64 — PASS. Native race и точечный core race — PASS; полный core race sweep не запускался. Android: 60 Kotlin tests, lint (0 errors, 1 warning), compileReleaseKotlin и debug APK — PASS. Последующие изменения касались только стенда и документов; изменённые fixture/window/boundary cases проверены точечно.

Отдельный синтетический model20 — **FAIL**, критерий сохранён без допуска:

| Фаза | Upload, Мбит/с | Download, Мбит/с | Upload последняя треть | Download последняя треть | Max app RTT, мс | Итог |
|---|---:|---:|---:|---:|---:|---|
| download | — | 7.842 | — | 8.205 | 1025 | PASS |
| upload | 12.579 | — | 12.019 | — | 1203 | PASS |
| duplex | 11.312 | 5.077 | 11.220 | **3.631** | 1355 | **FAIL** |

Во всех фазах по 60 независимых проб, 0 ошибок проб; неожиданных carrier exits в этой модели — 0. Duplex недостаточен по последней трети download. Два прогона не дают подтверждения стабильной производительности; delivery governor/window остаётся следующим этапом. Артефакт предназначен для локального экспериментального сравнения, стабильным или performance-qualified релизом не является. Проверка реального Android/TUN/VPS/provider — **NOT_RUN**.
