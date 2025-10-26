# JobMonitor

JobMonitor to lekki serwis w Go, ktory monitoruje wskazane jednostki systemd, zapisuje ich status co kilka minut do pliku JSON i prezentuje wyniki przez prosty web UI z kafelkami w stylu status page.

## Funkcje
- cykliczne sprawdzanie uslug przez `systemctl is-active` (opcjonalnie `sudo systemctl is-active`),
- historia zapisow w `.dist/data/status_history.json` (znacznik czasu UTC + wynik dla kazdej uslugi),
- API JSON: `/api/status`, `/api/history`, `/api/uptime`,
- dashboard pod `http://localhost:8080` z kafelkami i timeline ostatnich prob, odswiezany co 30 sekund.

## Konfiguracja
Stworz `config.yaml` w katalogu projektu i wpisz monitorowane uslugi:

```yaml
interval_minutes: 5
data_directory: .dist/data
targets:
  - id: tsunamibot
    name: Tsunami Bot
    service: tsunamibot.service
    timeout_seconds: 8
  - id: nginx
    name: Reverse Proxy
    service: nginx.service
```

Jesli dana usluga wymaga uprawnien administratora do sprawdzenia stanu, uruchom JobMonitor z odpowiednimi uprawnieniami (np. `sudo ./jobmonitor ...`). Mozesz tez ustawic `use_sudo: true` dla konkretnego celu, pamietaj jednak, ze `sudo` moze oczekiwac hasla, dlatego rekomendowane jest dodanie wpisu w sudoers lub uruchamianie programu jako root.

## Uruchomienie
```powershell
go build ./cmd/jobmonitor
./jobmonitor.exe -config config.yaml -addr :8080
```

- pierwszy pomiar wykonywany jest od razu po starcie, kolejne wedlug `interval_minutes`,
- logi przy starcie wyswietla liczbe uslug zaladowanych z konfiguracji.

## API i UI
- `/api/status` — ostatni zestaw wynikow (stan kazdej uslugi),
- `/api/history` — pelna historia z pliku JSON,
- `/api/uptime` — procent uptime, liczba prob i ostatni stan dla kazdej uslugi,
- `/` oraz `/static/*` — web UI z kafelkami, stanem bieżącym i timeline dla kazdej uslugi.

## Dane wyjsciowe
Przykladowy wpis w `status_history.json`:

```json
{
  "timestamp": "2025-10-26T15:00:00Z",
  "checks": [
    { "id": "tsunamibot", "name": "Tsunami Bot", "ok": true, "state": "active" },
    { "id": "nginx", "name": "Reverse Proxy", "ok": false, "state": "inactive", "error": "inactive" }
  ]
}
```

Pole `state` zawiera wynik `systemctl is-active` (np. `active`, `inactive`, `failed`). `ok` przyjmuje `true` tylko dla stanu `active`, a `error` przechowuje dodatkowe informacje z `systemctl`, jesli polecenie sie nie powiodlo.

## Wskazowki
- Uruchamiaj monitor na maszynie z systemd (Linux). Na innych platformach `systemctl` nie bedzie dostepne.
- Po zmianie listy uslug zrestartuj JobMonitor; historia pozostanie nienaruszona.
- Aby wyczyscic historie, zatrzymaj program i usun plik `.dist/data/status_history.json`. Przy kolejnym uruchomieniu odtworzy sie od zera.
