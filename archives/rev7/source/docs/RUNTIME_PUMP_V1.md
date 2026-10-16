# Владелец установленных TCP-потоков — runtime-1

## Исполняемый цикл

`datapath.StartPump` запускает один owner loop для Engine. Все изменения
byte accounting, окон, FIN, receipts, попыток записи и stream retirement
выполняются этим владельцем. Engine не вызывается конкурентно из работников.

`Pump.Add` принимает уже разрешённый и принятый Socket после успешного
аутентифицированного OPEN/OPEN_OK. При отказе socket закрывается, при успехе
владение полностью переходит Pump. Сам Add не авторизует пользователя и не
заменяет OPEN. Link не должен передавать frames ещё не зарегистрированных
потоков: startup/OPEN coordinator обязан обеспечить этот порядок.

Работники разделены: один приём Link, один отправитель управляющих сообщений,
один writer DATA; у каждого активного сокета — чтение и запись. Разрешено до128
активных сокетов. Входная очередь — 2×Streams+8 событий, очередь заданий DATA —
1 плюс выполняемый job; control queue настраивается в пределах2..1024. На
каждый socket read удерживается один блок до32KiB; следующий read разрешается
после приёма блока в Engine. У записи не более одного задания на сокет.
Лимиты буферов/metadata дополнительно задаются существующим Engine.Config.

Владелец не выполняет сетевые вызовы или fsync. Единственный callback в его
цикле — чистый Link.Size, не расходующий nonce и не выполняющий I/O. Link.Write
должен вызывать существующий durable Job.WriteLocal/WriteRemote ровно один раз.
Receive передаёт только аутентифицированные frames нужной generation либо
событие проверенной физической очистки. RECEIPT не заменяет Cleaned.

При заполненном окне отправки pending socket read сохраняется, а встречная
доставка продолжает работать. Если read больше доступного окна, владелец
уменьшает допустимый фрагмент до размера, принимаемого Engine, не теряя хвост.
FIN появляется только после принятия всех байтов последнего read. Порядок
обхода активных stream IDs меняется по кругу.

Socket write возвращает реальное n, включая случай n>0 вместе с error.
Delivered вызывается только для этого n; недописанный хвост при обычном short
write остаётся в RX. CloseWrite выполняется отдельным worker только после
полной доставки до peer FIN. Retire ждёт оба FIN, ACK локального FIN, receipt
всех отправленных байтов и доставку принятых байтов. Worker завершённого потока
получает отдельный stop; он не остаётся ждать до закрытия всей сессии.

При отмене Pump закрывает sockets, останавливает Engine, присоединяет workers
и освобождает очереди/ссылки на Link. Link I/O обязан соблюдать context.
Пока distributed RESET/final accounting не подключён, socket/transport error,
unknown APPEND, неверная generation или переполнение управляющей очереди
завершают ВСЮ epoch. Очередь не делает вид, что потерянный frame доставлен.
Физические расходы при ошибке остаются в journal/wallet. Это явно ограниченный
этап интеграции, а не финальная политика изоляции ошибок отдельных потоков.

## Приём почты без преждевременного cursor advance

`mailbox.Inbox` хранит одну bounded Page (до16 MIME, каждый до2MiB).
Next возвращает один untrusted Delivery с непрозрачным token, привязанным к
экземпляру Inbox. Пока владелец не вызвал Accept, следующий Next возвращает
IMAP_DELIVERY_PENDING. Повторный, нулевой или чужой token отклоняется.
Cursor страницы продвигается после передачи всех её сообщений владельцу;
ошибка FETCH и отмена до чтения не меняют его. Accept убирает ссылки Inbox,
но не очищает буфер, которым уже владеет caller.

Accept означает передачу владения, а НЕ аутентификацию, TCP-доставку, RECEIPT
или GC. Owner/Link обязан хранить raw MIME или успешно проверенный frame до
применения. Если DATA пришёл раньше UsageReport, нужен отдельный ограниченный
spool в session coordinator: нельзя выбрасывать DATA и нельзя навсегда
останавливать сканирование перед следующим control message. Этот spool пока
не реализован. Inbox не создаёт криптографический обход этого требования.

Отмена IMAP-операции на общей account connection может закрыть соединение.
Будущий account supervisor должен согласовать восстановление затронутых
сессий. Pump не реализует распределённый governor или reconnect этого уровня.

## Что именно доказано тестами

- Два одновременно работающих Pump, два TCP-потока с разными payloads,
  реальный TLS IMAP, Noise records, MailDispatcher и Inbox. По64KB upload и
  90KB download на поток, peer window16KiB, чтение блоками до32KiB, half-close.
  Передача после Add выполняется автоматически, без ручного вызова Engine
  для каждого DATA/receipt/window/FIN.
- В этой fixture используются отдельные local journal grants обоих endpoints;
  это проверка owner loop и существующего durable local writer. **Это не
  интеграция remote grant negotiation/continuousGC в Link.** Полный цикл
  remote grants/wallet/retirement отдельно проверяется сохранённым TLS IMAP
  integration test из dispatch-1.
- Заблокированный upload без WINDOW не мешает download; последующий WINDOW
  выпускает сохранённый upload. Unknown writer outcome останавливает epoch.
  Stale generation, control queue saturation и partial socket write проверены.
- Inbox удерживает неподтверждённое сообщение, не смешивает tokens разных
  экземпляров и не продвигает cursor после I/O failure.

## Оставшиеся соединения

В production исходниках есть Pump и Inbox; конкретный TLS IMAP Link для нового
Pump пока находится **в тестовой fixture**, а не в `serve`. `serve` остаётся
qualification probe. Нет нового рабочего CLI SOCKS режима и Android VPN backend.
Не заявляются скорость/RTT на Rambler или готовый пользовательский туннель.

Следующий блок должен связать production mailbox Link, startup/OPEN coordinator,
remote UsageReport/descriptor spool, обновление/retirement grants и непрерывный
GC, затем подключить SOCKS OpenFunc и expose CLI. Pump сейчас выбирает один
Account; планировщик работы по всем10аккаунтам/2пользователям также должен быть
подключён выше этого уровня. Новый HTML/UI не нужен.
