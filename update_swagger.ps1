# Script for updating Swagger documentation for all Novel Server services

Write-Host "Updating Swagger documentation for all services..." -ForegroundColor Green

# Function to generate swagger documentation
function Update-SwaggerDocs {
    param(
        [string]$ServicePath,
        [string]$MainFile,
        [string]$ServiceName
    )
    
    Write-Host "Updating documentation for $ServiceName..." -ForegroundColor Yellow
    
    if (Test-Path $ServicePath) {
        Push-Location $ServicePath
        
        # Check if swag is available
        if (!(Get-Command "swag" -ErrorAction SilentlyContinue)) {
            Write-Host "swag not found. Install it: go install github.com/swaggo/swag/cmd/swag@latest" -ForegroundColor Red
            Pop-Location
            return $false
        }
        
        # Generate documentation
        Write-Host "   Generating documentation..." -ForegroundColor Cyan
        $result = swag init -g $MainFile -o docs 2>&1
        
        if ($LASTEXITCODE -eq 0) {
            Write-Host "   Documentation for $ServiceName updated" -ForegroundColor Green
        } else {
            Write-Host "   Error generating documentation for $ServiceName" -ForegroundColor Red
            Write-Host "   $result" -ForegroundColor Red
        }
        
        Pop-Location
        return $LASTEXITCODE -eq 0
    } else {
        Write-Host "   Directory $ServicePath not found" -ForegroundColor Red
        return $false
    }
}

# Function to update dependencies
function Update-Dependencies {
    param(
        [string]$ServicePath,
        [string]$ServiceName
    )
    
    Write-Host "Updating dependencies for $ServiceName..." -ForegroundColor Yellow
    
    if (Test-Path $ServicePath) {
        Push-Location $ServicePath
        
        Write-Host "   go mod tidy..." -ForegroundColor Cyan
        go mod tidy
        
        if ($LASTEXITCODE -eq 0) {
            Write-Host "   Dependencies for $ServiceName updated" -ForegroundColor Green
        } else {
            Write-Host "   Error updating dependencies for $ServiceName" -ForegroundColor Red
        }
        
        Pop-Location
        return $LASTEXITCODE -eq 0
    } else {
        Write-Host "   Directory $ServicePath not found" -ForegroundColor Red
        return $false
    }
}

# List of services to update
$services = @(
    @{
        Name = "Auth Service"
        Path = "auth"
        MainFile = "cmd/auth/main.go"
    },
    @{
        Name = "Admin Service"
        Path = "admin-service"
        MainFile = "cmd/server/main.go"
    },
    @{
        Name = "Gameplay Service"
        Path = "gameplay-service"
        MainFile = "cmd/server/main.go"
    },
    @{
        Name = "WebSocket Service"
        Path = "websocket-service"
        MainFile = "cmd/server/main.go"
    },
    @{
        Name = "Swagger Aggregator"
        Path = "swagger-aggregator"
        MainFile = "cmd/server/main.go"
    }
)

$successCount = 0
$totalCount = $services.Count

# Update dependencies for all services
Write-Host ""
Write-Host "Updating dependencies..." -ForegroundColor Magenta
foreach ($service in $services) {
    if (Update-Dependencies -ServicePath $service.Path -ServiceName $service.Name) {
        $successCount++
    }
}

Write-Host ""
Write-Host "Generating Swagger documentation..." -ForegroundColor Magenta
$docsSuccessCount = 0

# Generate documentation for all services
foreach ($service in $services) {
    if (Update-SwaggerDocs -ServicePath $service.Path -MainFile $service.MainFile -ServiceName $service.Name) {
        $docsSuccessCount++
    }
}

# Final report
Write-Host ""
Write-Host "Update results:" -ForegroundColor Magenta
Write-Host "   Dependencies: $successCount/$totalCount services updated" -ForegroundColor $(if ($successCount -eq $totalCount) { "Green" } else { "Yellow" })
Write-Host "   Documentation: $docsSuccessCount/$totalCount services updated" -ForegroundColor $(if ($docsSuccessCount -eq $totalCount) { "Green" } else { "Yellow" })

if ($successCount -eq $totalCount -and $docsSuccessCount -eq $totalCount) {
    Write-Host ""
    Write-Host "All services successfully updated!" -ForegroundColor Green
    Write-Host ""
    Write-Host "Access to documentation:" -ForegroundColor Cyan
    Write-Host "   • Swagger Aggregator: http://localhost:8090/" -ForegroundColor White
    Write-Host "   • Auth Service: http://localhost:8081/swagger/index.html" -ForegroundColor White
    Write-Host "   • Admin Service: http://localhost:8084/swagger/index.html" -ForegroundColor White
    Write-Host "   • Gameplay Service: http://localhost:8082/swagger/index.html" -ForegroundColor White
    Write-Host "   • WebSocket Service: http://localhost:8083/swagger/index.html" -ForegroundColor White
    Write-Host ""
    Write-Host "To start all services: .\deploy.ps1" -ForegroundColor Cyan
} else {
    Write-Host ""
    Write-Host "Some services could not be updated. Check errors above." -ForegroundColor Yellow
    exit 1
}

Write-Host ""
Write-Host "Done!" -ForegroundColor Green 