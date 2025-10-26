# JobMonitor

JobMonitor to serwis napisany w Go, który monitoruje wskazane usługi systemd, zapisuje ich status co kilka minut do pliku JSON i udostępnia wyniki przez prosty web UI.

## Funkcje
- cykliczne sprawdzanie usług poprzez `systemctl is-active` lub (opcjonalnie) `sudo systemctl is-active`,
- historia w `.dist/data/status_history.json` archiwizująca każdy pomiar (czas UTC + wynik dla każdej usługi),
- API JSON: `/api/status`, `/api/history`, `/api/uptime`,
- wbudowane UI (`http://localhost:8080`) odświeżające dane co 30 sekund.

## Konfiguracja
Stwórz `config.yaml` w katalogu projektu i opisz usługi:

```yaml
interval_minutes: 5          # odstęp między pomiarami
data_directory: .dist/data   # gdzie zapisywać historię
targets:
  - id: tsunamibot
    name: Tsunami Bot
    service: tsunamibot.service   # nazwa jednostki systemd
    timeout_seconds: 8            # (opcjonalne) limit wykonania systemctl
  - id: nginx
    name: Reverse Proxy
    service: nginx.service
```

Jeżeli dana usługa wymaga uprawnień administratora do sprawdzenia stanu, uruchom JobMonitor z odpowiednimi uprawnieniami (np. `sudo ./jobmonitor ...`). Alternatywnie możesz ustawić `use_sudo: true` dla konkretnego celu, ale pamiętaj, że `sudo` może wymagać interakcji (hasło), więc rekomendowane jest nadanie programu odpowiednich uprawnień lub konfiguracja sudoers.

## Uruchomienie
```powershell
go build ./cmd/jobmonitor
./jobmonitor.exe -config config.yaml -addr :8080
```

- Pierwszy pomiar wykonywany jest od razu po starcie, kolejne zgodnie z `interval_minutes`.
- Logi startowe informują, ile usług zostało załadowanych z konfiguracji.

## API i UI
- `/api/status` – ostatni pomiar (stan każdej usługi).
- `/api/history` – pełna historia zapisów z pliku JSON.
- `/api/uptime` – zestawienie procentu uptime, liczby prób oraz ostatniego stanu dla każdej usługi.
- `/` oraz `/static/*` – prosty panel HTML/JS wyświetlający powyższe dane.

## Dane wyjściowe
Każdy wpis w `status_history.json` wygląda następująco:
```json
{
  "timestamp": "2025-10-26T15:00:00Z",
  "checks": [
    { "id": "tsunamibot", "name": "Tsunami Bot", "ok": true, "state": "active" },
    { "id": "nginx", "name": "Reverse Proxy", "ok": false, "state": "inactive", "error": "inactive" }
  ]
}
```

Pole `state` przechowuje wynik `systemctl is-active` (np. `active`, `inactive`, `failed`). `ok` przyjmuje `true` tylko wtedy, gdy stan to `active`. `error` zawiera dodatkowe informacje zwracane przez `systemctl`, jeśli sprawdzenie się nie powiodło.

## Wskazówki
- Uruchamiaj monitor na maszynie z systemd (Linux). Na innych platformach polecenie `systemctl` będzie nieobecne – w takiej sytuacji monitor zwróci błąd w kolumnie „Details”.
- Jeśli zmienisz listę usług, po restarcie aplikacji nowe elementy zostaną uwzględnione, a historia zachowa wcześniejsze wpisy.
- Aby zresetować historię, zatrzymaj program i usuń plik `.dist/data/status_history.json`. Przy kolejnym uruchomieniu utworzy się na nowo.
