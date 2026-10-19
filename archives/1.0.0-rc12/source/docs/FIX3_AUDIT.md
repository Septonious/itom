# Разбор журнала и исправления build-fix-3

Проанализирован весь предоставленный журнал `Pasted text(20260923-183853).txt` (269 строк), сопоставлены launcher, domain tests, validation, Gradle logging и код упаковки.

| Наблюдение | Вывод и действие |
|---|---|
| Строка 5: `ITOM launcher revision: windows-utf8-1` | Запущена старая ревизия. Механизм выбора старой копии по логу установить нельзя. Архив теперь имеет уникальное имя `_fix3.zip`, команда проверяет `--expect-revision build-fix-3` |
| 13 Python-тестов и source check проходят | Предыдущее исправление UTF-8 работает. Сохранено; cp1252 проверяется регрессионным тестом |
| Domain и его тесты компилируются | Ошибка происходит при выполнении тестов, а не при компиляции domain |
| Три прежних failure, 30 total | Общий положительный Invitation fixture в windows-utf8-1 содержит 34 hex символа pair_id при контракте 32. build-fix-2 исправлял значение; этот фикс включён и проверяется preflight |
| loginNewlinesRejected / invitationMustHaveExactSecretReferences | Неверный pair_id прерывает проверку раньше credentials. Код production validation не ослаблялся |
| invitationRoundTripAndSecretRedaction | Положительное приглашение должно быть валидным. Исправленный helper предварительно валидирует заготовку перед изменениями отрицательных сценариев |
| SDK XML warning (3 vs 4) | Это предупреждение в данном журнале. Сборка после него продолжилась; единственный зафиксированный FAILED task — domain:test. Обновление SDK/AGP для этой причины не требуется |
| 47 tasks executed, в том числе ресурсы и манифесты | Эти стадии отработали, но app Kotlin compile/lint и APK этим журналом не подтверждены |
| Лишь ComparisonFailure без expected/actual, затем длинный Gradle stacktrace | FULL test exception output сохраняется из fix2. Дополнительно сохраняется test-summary.json, где есть type/message, и печатается краткая причина. Внутренний Gradle stacktrace опционален |
| У всех прошлых архивов одно имя | Самораспаковка создавала новые папки, но не могла обновить скачанный архив. Новый sandbox/download filename отличается; предыдущие файлы не удаляются |

## Содержание фикса

- Уникальный файл `ITOM_PHASE2_SOURCE_0.4.0-dev1_fix3.zip` и новая метка `build-fix-3`.
- Общий `source-info.json`, ранняя проверка ожидаемой ревизии, SHA-256 архива в выводе и логах.
- Исправления UTF-8 и 32-символьного pair_id, проверка malformed positive fixture до Gradle.
- Все 30 Kotlin-тестов остаются включёнными; нельзя получить APK в обход domain:test/lint.
- Диагностика JUnit XML: сообщения ComparisonFailure/DomainError, broken/missing report не маскируют ошибку сборки; старые failure не печатаются как текущие без текущего `domain:test FAILED`.
- 18 offline/Python регрессионных проверок; финальный ZIP проверен через собственный launcher в режиме check.

## Что не подтверждено

APK/Gradle/Kotlin не собирались в чате по договорённости пользователя. Новый журнал пока не проверяет fix2 или fix3: в нём явно работает windows-utf8-1. Прохождение всех Kotlin-тестов, Android compile/lint и работа на устройстве будут подтверждены только повторной локальной сборкой. G2 остаётся открыт.
