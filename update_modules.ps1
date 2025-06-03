# Скрипт для обновления всех Go модулей в проекте
Write-Host "Обновление Go модулей..." -ForegroundColor Green

# Список всех сервисов
$services = @(
    "shared",
    "auth", 
    "gameplay-service",
    "admin-service",
    "story-generator",
    "websocket-service", 
    "notification-service",
    "image-generator",
    "swagger-aggregator",
    "api-gateway"
)

# Обновляем каждый сервис
foreach ($service in $services) {
    Write-Host "Обновление $service..." -ForegroundColor Yellow
    
    if (Test-Path $service) {
        Set-Location $service
        go mod tidy
        if ($LASTEXITCODE -eq 0) {
            Write-Host "✓ $service обновлен успешно" -ForegroundColor Green
        } else {
            Write-Host "✗ Ошибка при обновлении $service" -ForegroundColor Red
        }
        Set-Location ..
    } else {
        Write-Host "✗ Директория $service не найдена" -ForegroundColor Red
    }
}

Write-Host "Обновление завершено!" -ForegroundColor Green 