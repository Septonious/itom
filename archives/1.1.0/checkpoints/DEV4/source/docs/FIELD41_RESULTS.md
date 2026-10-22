# FIELD41 — результаты

Revision: transport-s1-perf5-field20. Статус: кандидат для реального APK/VPS прогона на базе рабочего FIELD40.

FIELD40 сохранён: реальное наблюдение пользователя40–50+Мбит/с и сохранение почтовых линий. Реальный обрыв спидтеста ещё не объявляется устранённым.

## Финальная модель130Мбит/с,0loss

Локальный TLS IMAP, production supervisors, HTTP/TLS, каждый application byte проверяется. Старый client-only профиль FIELD12 mirrored на server; это не точное воспроизведение Rambler и не измерение Android.

| Режим | DL Мбит/с | UL Мбит/с | Tail DL | Tail UL | Max RTT мс |
|---|---:|---:|---:|---:|---:|
|BINARY, download|117.507|0.000|117.988|0.000|456|
|BINARY, upload|0.000|82.932|0.000|88.241|562|
|BINARY, duplex|105.599|79.769|106.723|84.466|505|
|20 потоков, download|116.680|0.000|119.199|0.000|744|
|base64, download|83.509|0.000|85.165|0.000|572|

Все финальные измерительные сценарии:PASS; minimum75Мбит/с по активному направлению и tail, HTTP RTT<1000мс.120 provider sockets accepted,0 dropped,0 spontaneous carrier exits. При full duplex направления измеряются отдельно; aggregate не заменяет направление.

BINARY уменьшает входящий base64-трафик. APPEND остаётся прежним;100+Мбит/с достигнуты в модели на download, не на upload. С поддержкой BINARY конкретных Rambler аккаунтов нужен новый полевой прогон. Для несовместимых серверов остаётся обычное BODY чтение.

## Проверки

| Проверка | Результат |
|---|---|
| Затронутые datapath/mailbox и выбранные tunnel сценарии, native |95 верхнеуровневых случаев,0fail|
| Те же компоненты и выбранные сценарии под race |95 верхнеуровневых случаев,0fail,0data race|
| Фактический gonet/tun2socks мост |6 half-close запросов, целостность, отмена7-го активного TCP:PASS normal/race|
| Плановый rekey с сохранением TCP/owner |PASS normal/race|
| BINARY, UNKNOWN-CTE, generic NO, provider header rewrite |PASS; MIME побайтно совпадает, та же connection, delete proof|
| Потоковая выдача до tagged completion и ошибки/отмена |PASS|
| go vet затронутых компонентов и native |PASS|
| Linux amd64/arm64 daemon |PASS compile; CLI field20|
| Windows amd64 tunnel test compilation |PASS compile, не Windows execution|
| Native Go Android arm64 |PASS compile; не JNI/AAR/APK|
| Offline source/package checks |65Python checks:PASS;8Python files,11XML;57Kotlin tests только присутствуют|

Полный Go suite не повторялся: по последней инструкции пользователя выполнены проверки затронутых компонентов и выбранных интеграций. Android Gradle/unit/lint/device gates здесь не выполнялись. Launcher all выполняет эти gates при вашей сборке. Прежние447PASS FIELD40 не присваиваются FIELD41.

## Сравнение и отклонённые варианты

Неизменный FIELD40 в новом запуске:DL84.096/maxRTT526, UL81.876/maxRTT576. Duplex DL75.305/UL70.517/max613 с tails ниже75 — этот baseline run не выдаётся за успешный скоростной gate. Его реальные40–50+ и стабильные линии сохраняются как рабочая полевая база.

Финальный обычный base64 download83.509/max572: скорость−0.7%, RTT+8.7% к указанному DL baseline. Это один парный запуск, не статистическое доказательство отсутствия регрессии. Улучшение обеих метрик наблюдается в BINARY режиме; прирост реального сервиса не заявляется.

Cold pacing80, cached NACK replay и ранний STATUS ухудшали скорость до62–74Мбит/с; отвергнуты. Recovery/grace/pacing/quotas/rekey сохранены из FIELD40. Неудачные промежуточные прогоны включены в архив доказательств и явно отделены от финальных release-* logs. Первый native test harness ошибочно передавал outbound PacketBuffer как inbound и зависал; исправлены CloneToInbound и cancel-aware Accept. Это ошибка тестового стенда, не подтверждение production timeout.

Аудит находится в docs/FIELD41_STABILITY.md. Архив логов содержит команды, exit codes, SHA256 Go snapshot и бинарных сборок, финальные/промежуточные журналы. Входные пользовательские архивы трасс и реальные профили не включены в source/test bundle.
