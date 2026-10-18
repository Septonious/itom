# core-1-locked-1 · 25 сентября 2026

Checkpoint после получения четырёх файлов пользователя:
core-report.json, core-go.mod, core-go.sum, core-modules.json.

Сверены revision, SHA-256 исходного архива, версия и checksum toolchain,
набор семи ожидаемых пакетов и три закреплённые версии зависимостей.
В отчёте: status=passed, stage=complete, 54 passed test events, 0 failed/skipped,
vet=passed. Race выключен, fuzz запускал только seed corpus; APK/server/IMAP
не собирались и не проверялись. Число 54 включает подтесты и fuzz seeds.

core/go.mod и core/go.sum скопированы из приложений пользователя без изменения
их байтов. go.mod семантически совпадает с прежним (отличается только завершающим
пустым рядом). go.sum содержит 19 корректно оформленных h1-записей, в том числе
module и go.mod sums для трёх закреплённых модулей. Формат и присутствие entries
проверены здесь; сетевую верификацию checksum database здесь повторно не выполняли.

Исходные приложения сохранены без изменения в reports/core1-windows-20260925,
provenance.json содержит их SHA-256 и ссылку на исходную revision/hash.
Module graph содержит 11 модулей; он является снимком разрешённого графа, не
списком исключительно production/runtime зависимостей.

Go-код, wire fixture, Android и сборщик не менялись. Новая source revision
отличает поставку с go.sum от core-1-fix-1. Полный новый Go gate здесь не запускался;
исходный успешный отчёт относится именно к пользовательскому core-1-fix-1.
Исторические reports/CORE1_SOURCE_CHECKS.json и CORE1_FIX1_CHECKS.json оставлены
как результаты соответствующих этапов, а не нового Go запуска.

Следующая работа: полные control DTOs и FSM/actor, durable journal и физические
бюджеты, generic IMAP, сервер, затем Android datapath по принятому концепту.
