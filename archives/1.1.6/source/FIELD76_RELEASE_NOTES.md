# ITOM FIELD76 — согласованный исходный комплект

Дата: 2026-10-10. Ревизия Android и Linux: `transport-s1-perf7-field76`. Версия Android debug: `0.9.0-dev2-field76-debug`, `versionCode=9076`, package: `org.itom.client.dev`.

## Содержимое

- Безопасная изоляция одного неподтверждённого DATA доказательства при `JOURNAL_SCOPE` во время `reclaimBounded`, без фиктивного списания долга и удаления сообщений.
- Исправление карантина DATA при одинаковых отметках времени.
- Ограниченное повторное подключение DATA writer только **до** начала физического IMAP APPEND.
- Согласованный номер FIELD76 на Android, Linux, trace и исходном архиве. Это устраняет неоднозначность разных сборок под общей меткой FIELD72.
- Автоматическое включение обязательных тестов восстановления сети; увеличен общий Go package timeout до 420 с без ослабления собственных проверок.
- Расширенная диагностика этапов Android после Gradle (`post_gradle_domain_and_lint_gate`, `post_gradle_apk_identity`, `verified_apk_export`). Нельзя трактовать `BUILD SUCCESSFUL` как завершённую поставку до `dist/build-report.json: status=passed`.

## Сборка (Windows PowerShell)

```powershell
python .\ITOM_TRANSPORT_FIELD76_SOURCE.zip all --expect-revision transport-s1-perf7-field76 --accept-android-licenses
```

Сборка сначала выполняет Go gate, затем Android, сверяет Gradle JUnit / lint и сравнивает package/version APK по metadata и `aapt2`. Лишь после этого отправляет `ITOM_FIELD76_debug.apk` и Linux-бинарники в `dist` **рядом с ZIP**. Не устанавливать `android/app/build/outputs/apk/debug/app-debug.apk`, если общая сборка вернула ошибку: этот файл может оставаться после неуспешного gate.

Если `BUILD SUCCESSFUL` сменяется `APK build failed`, первым делом проверить `build-report.json` поля `status`, `failed_stage`, `error`; затем `build.log`, `lint-summary.json` и `test-summary.json` в распакованной каталоге текущего ZIP. Непроверенный APK не считается результатом выпуска.

## Установка сервера

ПЕРЕД установкой остановить службу и создать полную резервную копию `/var/lib/itom`, сохранив все DATA/control/wallet базы, а также сохранить предыдущее `/usr/local/bin/itom`. Не очищать почтовые папки, журналы или базы. Обновлять Android и сервер из **одного** исходного ZIP, не использовать старый FIELD72 APK с новым сервером.

```powershell
scp .\dist\ITOM_FIELD76_linux_amd64 root@YOUR_VPS:/tmp/ITOM_FIELD76_linux_amd64; if ($LASTEXITCODE -eq 0) { ssh root@YOUR_VPS "chmod 755 /tmp/ITOM_FIELD76_linux_amd64 && /tmp/ITOM_FIELD76_linux_amd64 version && /tmp/ITOM_FIELD76_linux_amd64 install" }
```

Для APK:

```powershell
adb install -r .\dist\ITOM_FIELD76_debug.apk
adb shell dumpsys package org.itom.client.dev | findstr /i "versionName versionCode"
```

## Ограничения квалификации

Синтетические тесты нельзя считать подтверждением работы реального IMAP-провайдера и Android устройства. До принятия в промышленный релиз требуется проверить полное восстановление 10 аккаунтов после сохранённой аварии, запуск Android, отсутствие `JOURNAL_SCOPE`-циклов и последовательно 3 нормальных теста с >=30/30 Мбит/с и RTT <800 мс, плюс профили сети с отказами. Не удалять persistent state для получения «чистого» результата.
