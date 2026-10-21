# Source20: рабочий кандидат, не релиз

После сбоя восстановлен сохранённый source14; изменения source17–19 заново реализованы и проходят повторную проверку, исторические результаты не приписываются source20. Source15/16 эксперименты не включены.

Active sibling streams удерживают dedicated reader в bounded polling вместо лишних IDLE/DONE переходов; quiet reader возвращается в IDLE. UID-only/FLAGS/MODSEQ FETCH updates не крадут BODY. Journal profile12MiB допускает ровно одну fenced bulk replacement при двух базовых leases, remote share6.5MiB/local5.5MiB; fast reserve и общий quota не расширяются. Legacy8MiB неизменён. Percentile сортирует копию, observer сохраняет все причины carrier exits.

Новое требование: loss>0 до5% — номинальные25 полезных Mbps отдельно в каждом направлении,22.5 с10% люфтом, hard RTT3000ms, без таймаутов/порчи/самопроизвольных carrier exits. Для loss=0 прежние60/30Mbps и hard1000ms,125 goal остаются. При физической ёмкости ниже номинального floor проверяется непрерывность и сравнение с field4; это не достижение25/125.

Windows выполнение, Android compilation/device и реальный provider не подтверждены Linux Go стендом.
