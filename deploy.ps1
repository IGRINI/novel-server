# deploy.ps1

# Загружаем переменные из .env в текущую сессию
Write-Host "Загрузка переменных из .env..."
(Get-Content .env) | Foreach-Object { 
    if ($_ -match '^([^#\s].*?)\s*=\s*([^#]*?)\s*(#.*)?$') { 
        $key = $Matches[1].Trim()
        $value = $Matches[2].Trim() # Берем только значение до комментария
        # Убираем возможные кавычки в начале/конце значения
        if (($value.StartsWith('"') -and $value.EndsWith('"')) -or ($value.StartsWith("'") -and $value.EndsWith("'"))) {
            $value = $value.Substring(1, $value.Length - 2)
        }
        # Устанавливаем переменную только если значение не пустое
        if (-not [string]::IsNullOrWhiteSpace($value)) {
            Write-Host "Environment variable: $key"
            Set-Content "env:\$key" $value
        } 
    } 
}

# Запускаем деплой стека
Write-Host "Запуск docker stack deploy..."
docker stack deploy -c docker-compose.yml novel_stack --with-registry-auth

Write-Host "Deployment started." 