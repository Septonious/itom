# ITOM field3 — logical owner и повторный account rejoin

Ревизия: `transport-s1-perf5-field3`, `0.9.0-dev1`. Основа — точный field2;
старый handoff сохранён в PROJECT_HANDOFF_FIELD2.md. Это development candidate,
не полная sustained/device квалификация. Governor выключен.

Объединённый выпуск содержит R06 и основной код R08–R10, а также причинно
необходимый retry scheduler fix. Один logical actor/Pump/TCPExit на account и
logical session остаётся жив при закрытии физической линии. Замена линии
использует прежний actor и лимиты; новые TCP-потоки не создают второй бюджет.
Срок жизни потока ограничен parent/session shutdown и прежним 25-секундным
watchdog по аутентифицированному remote progress. Статистика TCP учитывает
logical owner после rejoin.

Потоковые control и DATA после аутентификации направляются владельцу stream ID
в той же user/session/generation. Физические grants, Usage, GC и journals
сохраняют account scope. ITH9 требует новый exact capability LOGICAL_CONTROL;
старый field2 peer отклоняется. Обновлять APK и VPS только вместе из одного
успешного `all` build. Фрейминг version 9 и DATA/control formats сохранены.

NotCommitted retry допустим только до APPEND и при успешной отмене Prepared
reservations. Scheduler повторяет тот же byte range. Unknown APPEND сохраняет
физический долг и использует аутентифицированный repair; wallet descriptors
остаются durable при потере физического control. Перед unlock журналов Close
join-ит admitted DATA writers. Cross-carrier links теперь включают собственные
physical attempts старого owner, чтобы replacement recovery направил только
проверенный physical cleanup соответствующему byte engine.

Лимиты остаются прежними: logical/account/receive 4 МиБ, 64 sockets, 4096
metadata; globally 4096 odd stream IDs. Суммарная incoming очередь остаётся
256 frames: 32 actor + 224 Pump. Повторные carrier epochs не создают новый
logical budget. Journals сохраняют existing 8/128 и fresh 12/192 profiles;
автоматической миграции, reset или переноса долга между аккаунтами нет.

Go-проверки охватывают byte-preserving definitive retry, capability rejection,
100 repeated epochs без нового logical budget, routing control к detached
owner, watchdog progress, реальные supervisor/TLS/IMAP outages на 2/5 секунд,
тот же application socket и один target TCP dial, затем all-account loss с
закрытием по watchdog. Точные команды, серии, FAIL/PASS и ограничения — в
отдельном ITOM_FIELD3_REPORTS.zip; не подменять этим APK/Windows/device tests.

R07 и полная loaded fault/sustained приёмка **не закрыты**. Диагностические
2/4-account runs обнаружили probe timeout и отсутствие usable bulk grants при
READY и заполненном DATA journal. Испытанная admission/cleanup модификация
тоже не прошла: сохранена в experimental/FIELD3_SERVICE_CANDIDATE.patch.disabled
и исключена из runtime. Следующая работа объединяет R07 с R13/R14: доказать
fair service для local/remote debt, usable grant refill, cleanup и fast DATA,
не расширяя exposure и не возвращая квоту без physical proof.

Сборка в PowerShell из каталога с source ZIP:

```powershell
python .\ITOM_PHASE4_SOURCE_0.9.0-dev1_transport-s1-perf5-field3.zip all --expect-revision transport-s1-perf5-field3 --accept-android-licenses
```

После успешной сборки синхронной пары и загрузки Linux binary в /tmp:

```bash
chmod 755 /tmp/itom-linux-amd64 && sudo /tmp/itom-linux-amd64 install
```

Само по себе systemd active не подтверждает transport readiness. Existing
server state и dirty DB сохраняются. Полная Windows/Android/device/provider
приёмка, capacity/fairness/proof bounds и длительный soak остаются в grouped
плане; не ослаблять 30 Mbps/700 ms legacy gates.


Допуск replacement carrier к DATA и stream control требует matching reply на
новый authenticated Echo его текущего crypto epoch. READY/старый RTT сами по
себе не подтверждают rejoin. Echo стоит в normal FIFO после CoordinatorAck,
поэтому не обгоняет bootstrap. Потоковая маршрутизация не обходит Gate/Bootstrap
activation checks. До fresh echo используются уже готовые sibling carriers;
никаких дополнительных физический квот или продления watchdog нет.


После CommitUnknown отправитель может приблизить один status-probe в пределах
обычного ProbeInterval (2 секунды), чтобы не добавлять полный periodic interval
к восстановлению. Остальные Unknown completions того же APPEND не создают
неограниченный burst. Unknown не становится NotCommitted, долг сохраняется;
DATA repair по-прежнему требует authenticated NACK. Watch deadline и transport
watchdog не продлеваются. Регрессия проверяет ранний probe, отсутствие refund/
blind retry, авторизацию через настоящий receiver response и bounded burst.
