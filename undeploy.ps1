# undeploy.ps1

Write-Host "Удаление стека novel-server..."
docker stack rm novel-server

Write-Host "Стек удален. Ожидание завершения..."
Start-Sleep 10

Write-Host "Готово." 