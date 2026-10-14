# Вторая фаза: 0.4.0-dev4 / ui-final-1

Дата: 2026-09-24. Пользователь принял внешний вид и поручил удалить демо, назвать приложение ITOM, провести аудит и завершить этот этап. Этап UI/Android-оболочки закрыт по исходникам; сетевой G2 не закрыт. APK и будущие серверные бинарники пользователь собирает локально.

| Область мастер-плана | Состояние |
|---|---|
| F2.1 Android skeleton | Общие main sources для обоих backend-вариантов, compile/target 36, min 29; новая локальная сборка ожидается |
| F2.2 Mock/lifecycle | Runtime mock полностью удалён; reducer и generation guards сохранены для настоящего ядра |
| F2.3 UI | 10 native Compose screens, полноценный editor и app picker; оформление принято пользователем и зафиксировано |
| F2.4 Storage | AES-GCM/Keystore, vault v2, сохранение изменённых legacy profiles, проверка записи, revision guard; device QA ожидается |
| F2.5 Pairing | Валидируемый JSON/QR-image import; серверная выдача приглашения ещё не реализована |
| F2.6 VpnService/TUN | Не реализованы; общий backend сразу возвращает CORE_NOT_INTEGRATED |
| F2.7 App routing/network binding | Каталог/политики и чистый classifier готовы; применение к трафику, protect/bind/DNS ожидают core |
| F2.8 Foreground/system lifecycle | Демонстрационные FGS/Stop/permissions удалены; QS tile открывает UI. Настоящий VPN service/notification ещё впереди |
| F2.9 IMAP diagnostics | Не реализованы; диагностический экран не проверяет почтовую сеть |
| F2.10 Diagnostics | Локальный экспорт разрешённых полей без секретов, идентификаторов профиля и адресов |
| F2.11 Benchmark | Не реализован; синтетические метрики удалены |
| F2.12 Packaging | Source ZIP + launcher и строгие локальные build gates; production signing и Go AAR ещё впереди |

Здесь выполнены 32 Python/offline-теста; 52 Kotlin-теста подготовлены для локального `:domain:test`. Compile/Lint/device tests новой ревизии не выполнялись. Принятие оформления пользователем не подменяет результаты проверки ui-final-1.

## Следующий инкремент

1. Учесть результат локальной команды и проверить обновление/миграцию по DEVICE_CHECKLIST. Без нового запроса не начинать визуальный redesign.
2. Подключить Go core/AAR и TUN adapter: контролируемый TUN→SOCKS datapath, затем IMAP. Отдельный диагностический режим не должен включаться незаметно.
3. Реализовать VpnService consent/revoke, foreground notification со скоростями/RTT/Stop, protected sockets, explicit network binding, закрытие TUN перед terminal Error, перезапуск по новой revision и защиту от поздних callbacks.
4. Подключить per-app Builder allowed/disallowed lists, shared UID, package removal, Android user/work profile и lockdown; не смешивать allowed/disallowed, запрещать empty include.
5. Реализовать DNS mapping и фактический classifier в порядке VPN → Block → Direct. IPv6 transport/произвольный UDP и автоматический Direct fallback не включать до отдельного решения/квалификации.
6. Реализовать явные IMAP TLS/AUTH/CAPABILITY и тестовые APPEND/FETCH/IDLE с очисткой выделенных папок и redaction. Не выполнять тяжёлые проверки автоматически при открытии UI.
7. Добавить настоящий benchmark с отменой, потоками 1/4/8/16/32/64 и раздельным учётом полезных данных/повторных передач.
8. Реализовать серверную регистрацию, уникальные восьмизначные user ID, ключи, namespace и приглашения. Затем source server target и команда обновления VPS из `/tmp`.

G1 в нереализованной части Go/wire/Noise/network и G2 остаются открытыми. UI-final не меняет транспортные параметры и не доказывает скорость/устойчивость ITOM.
