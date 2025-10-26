# JobMonitor

JobMonitor to lekki serwis w Go, ktory monitoruje wskazane jednostki systemd, zapisuje ich status do historii JSON i prezentuje wyniki przez web UI w stylu status page. Od teraz kazdy wezel moze pobierac dane z innych instancji JobMonitor, dzieki czemu na jednym pulpicie widac stan wielu serwerow.

## Funkcje
- cykliczne wywolanie `systemctl is-active` (opcjonalnie z `sudo`) dla kazdej uslugi,
- zapis historii prob w `.dist/data/status_history.json` (czas UTC + wynik dla kazdej uslugi),
- lokalne API JSON (`/api/node/status`, `/api/node/history`, `/api/node/uptime`) oraz zbiorcze `/api/cluster`,
- dashboard pod `http://localhost:8080` z kafelkami serwerow, przebiegiem uptime i lista incydentow,
- agregacja danych z wielu wezlow JobMonitor i wyswietlenie ich na jednym pulpicie.

## Konfiguracja
Stworz `config.yaml` w katalogu projektu. Minimalny przyklad z dwoma serwerami:

```yaml
interval_minutes: 5
data_directory: .dist/data
node_id: node-a                 # unikalny identyfikator wezel
node_name: Serwer A             # nazwa wyswietlana w UI
peer_refresh_seconds: 60        # co ile sekund odswiezac peerow
targets:
  - id: tsunamibot
    name: Tsunami Bot
    service: tsunamibot.service
    timeout_seconds: 8
  - id: nginx
    name: Reverse Proxy
    service: nginx.service
peers:
  - id: node-b
    name: Serwer B
    base_url: http://192.168.55.120:8080
    enabled: true
    # api_key: opcjonalny klucz jesli endpointy sa chronione
```

**Wskazowki konfiguracyjne**
- `node_id` musi byc unikalne w ramach klastra. Domyslnie przyjmowana jest nazwa hosta.
- `peers` to lista innych instancji JobMonitor. Kazdy wpis okresla adres bazowy oraz opcjonalny klucz API (przesylany w naglowku `Authorization: Bearer`).
- Jesli do odczytu stanu uslugi wymagane sa uprawnienia roota, uruchom JobMonitor jako root albo ustaw `use_sudo: true` przy konkretnej definicji w `targets`.

## Uruchomienie
```powershell
go build ./cmd/jobmonitor
./jobmonitor.exe -config config.yaml -addr :8080
```

- pierwszy pomiar wykonywany jest natychmiast po starcie, kolejne wedlug `interval_minutes`,
- w logu startowym zobaczysz liczbe monitorowanych uslug oraz identyfikator wezel,
- agregator peerow startuje automatycznie i co `peer_refresh_seconds` pobiera dane z podanych instancji.

## API i klastrowanie
- `/api/status`, `/api/history`, `/api/uptime` - wsteczne kompatybilne endpointy z danymi lokalnymi,
- `/api/node/status`, `/api/node/history?limit=200`, `/api/node/uptime` - dane jednego wezla (w odpowiedzi zawsze znajduje sie `node.id` i `node.name`),
- `/api/cluster` - zlaczony widok lokalnego wezla oraz wszystkich skonfigurowanych peerow (to z tego endpointu korzysta UI),
- odpowiedzi JSON zawieraja timeline pomiarow, gotowe statystyki uptime i info o ewentualnych bledach synchronizacji.

## Dane wyjsciowe
Przykladowy rekord w `status_history.json`:

```json
{
  "timestamp": "2025-10-26T15:00:00Z",
  "checks": [
    { "id": "tsunamibot", "name": "Tsunami Bot", "ok": true, "state": "active" },
    { "id": "nginx", "name": "Reverse Proxy", "ok": false, "state": "inactive", "error": "inactive" }
  ]
}
```

Pole `state` zawiera wynik `systemctl is-active` (np. `active`, `inactive`, `failed`). `ok` przyjmuje `true` tylko dla stanu `active`, a `error` przechowuje tresc zwrocona przez `systemctl` kiedy polecenie zakonczylo sie status > 0.

## Wskazowki eksploatacyjne
- Uruchamiaj monitor na maszynie z systemd (Linux). Na innych platformach polecenie `systemctl` bedzie niedostepne.
- Aby oczyscic historie, zatrzymaj serwis i usun `.dist/data/status_history.json`; po restarcie plik zostanie utworzony ponownie.
- Jesli konfiguracja peerow zawiera klucz API, zadbaj o zsynchronizowanie go na wszystkich wezlach.
- W UI lista incydentow wyswietla bledy ostatnich prob oraz ewentualne problemy z synchronizacja peerow.
