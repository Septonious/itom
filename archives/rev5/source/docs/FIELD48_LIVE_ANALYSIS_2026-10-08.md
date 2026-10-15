# FIELD48: анализ полевого отказа 2026-10-08

На обеих сторонах подтверждена transport-s1-perf7-field27. Пользователь сообщает: обычный спидтест завершается ошибкой, idle RTT около 500 ms. Скриншот альтернативного веб-теста показывает download 9.69 Mbps, upload 8.03 Mbps, latency 574 ms, jitter 246 ms; в момент снимка ещё выполняется upload 10 MB. Это не фиксированный квалификационный интервал и не доказательство устранения регрессии.

## Границы записи

Клиент: 08:25:33.425–08:28:56.253 UTC, первая сохранённая seq 16210. Сервер: 08:26:27.219–08:29:33.778 UTC, первая seq 45519. Начало сессии уже вытеснено из трёх кольцевых файлов. Нельзя восстановить весь первый запуск спидтеста или сопоставить каждый отказ приложения с полной серверной историей.

Часы Android и VPS не синхронны: встречаются отрицательные интервалы client APPEND → server FETCH, а противоположное направление выглядит искусственно длиннее. Сырые односторонние интервалы не использовать как задержку доставки. PING → PONG на одном и том же хосте не зависит от сдвига: в общем сохранённом участке client p50 365 ms / p90 1040 ms / max 1253 ms, server p50 391 ms / p90 746 ms / max 1742 ms. Это смесь нагрузки и простоя, а не чистый idle benchmark.

## Подтверждённые наблюдения

- Все 21 видимое клиентское OPEN на TCP 8080 получили OPEN_OK. Короткие обмены происходили, затем Android-мост записывал upload RESET и download CLOSED. В разобранных примерах вслед за этим отправлялся FIN, без предварительного полученного transport RESET. Это не доказательство обрыва IMAP. Старый application_copy_end не различает ошибку чтения и записи, поэтому источник самого TCP reset окончательно не установлен.
- Один OPEN на TCP 443 завершился server DNS failure (4 ms DNS); client получил OPEN_ERROR code 7 за 303 ms. Имя редактировано. Нельзя объявить этот единичный случай причиной всех отказов спидтеста.
- На клиенте 14512, на сервере 54535 отказов delivery_admission_wait с generic IO_OR_PROTOCOL_ERROR. Все клиентские и почти все серверные — bulk; сами проверки занимают микросекунды. По коду основной нефатальный путь здесь — future-only deferred batch. Generic code не различает deferred и stopped; FIELD49 добавляет явные коды. Это признак повторного расписания, а не 54 тысяч сетевых ошибок.
- Успешные control APPEND: client p50 52.55 ms, server p50 65.48 ms; FETCH page p50 16.16/29.87 ms. Очередь и lifecycle добавляют существенную задержку поверх отдельных команд.
- Client reader repairs в health — 0. Два unknown control APPEND в 08:28:56 имеют local canceled close и совпадают с trace_stop. Серверная волна TRANSPORT_TIMEOUT в 08:29:20 идёт уже после отключения клиента. Эти события не доказывают причину раннего отказа обычного спидтеста. Два client DATA writer временно теряют одну доступную DATA connection и восстанавливаются примерно через секунду.

## Воспроизведённый дефект byte Engine

После Begin → Complete(NotCommitted) item вновь Queued. Watch/attempted frontier уже существуют, и запоздалый STATUS/NACK может обозначить отсутствующий диапазон. FIELD48 принимал такой NACK как authorization для BeginRepair, но flow запрещает BeginRepair на Queued item: результат FLOW_STATE завершает owner. Более длительное ожидание deferred items делает этот старый переход воспроизводимым. Изолированный regression test с engine.go FIELD48 падает; с FIELD49 проходит.

NACK теперь пропускает Queued items: за ними уже закреплена первоначальная отправка, не требующая physical repair. Writing/VisibleWait/CommitUnknown сохраняют прежнюю authenticated repair policy и physical debt. Не утверждается, что именно этот дефект доказан причиной конкретной ошибки интерфейса спидтеста: её начало не сохранилось.

## FIELD49

Future-only DATA возвращается actor с broadcast token поколения delivery predicate; actor не создаёт новое физическое задание до изменения predicate. Workers и mail slots свободны, bytes остаются у Engine, cancellation не ждёт токен. Partial batch перед парковкой перепроверяет готовность под group mutex, чтобы не потерять ранний ACK.

Короткий клиентский DATA primes два deterministic quiet receive stripes. Fresh request также прерывает empty-poll sleep; burst wake coalesces в один slot. Request hot budget учитывает последний локальный Echo RTT и ограничен 2 s. Серверный reply DATA и repairs не создают reciprocal polling. Бюджет bulk 1–3 MiB и все physical quota/fsync/unknown правила сохранены.

Native TCP copy теперь отмечает read/write/eof/half_close stage, не раскрывая адреса или ошибочный текст. Это позволит отделить reset со стороны Android-приложения от записи в upstream при следующей полевой проверке. UDP, кроме DNS, остаётся неподдержанным по текущему контракту; без данных о конкретном приложении не объявлять это причиной отказа.
