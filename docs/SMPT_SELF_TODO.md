# 🚀 Go-MailShield: Roadmap der Entwicklung (Roadmap & TODO)

Diese Datei enthält den aktuellen Status und den schrittweisen Entwicklungsplan des Pet-Projekts eines Email-Security-Gateways (Email Security Gateway) in Go.

---

## 🟢 ETAPPE 0: Infrastruktur und Basis-MVP (ABGESCHLOSSEN)

- [x] **SMTP Receiver:** Grundlegender E-Mail-Empfang über das SMTP-Protokoll in Go implementiert (`smtpd`).
- [x] **MIME Parser:** Parsing von Headern, Text und HTML von E-Mails integriert (`enmime`).
- [x] **Worker Pool:** Nebenläufige Verarbeitung von E-Mails über Channels (`chan EmailJob`) und einen Goroutine-Pool implementiert.
- [x] **Basis-SPF:** Suche nach DNS-TXT-Einträgen zur initialen Absenderprüfung implementiert.
- [x] **Docker & Compose:** Anwendung als Docker-Image mit mehrstufigem Build (multi-stage build) verpackt.
- [x] **CI/CD Pipeline:** GitHub-Actions-Workflow eingerichtet (Testlauf, Image-Build, Veröffentlichung in GHCR).
- [x] **GitOps Auto-Deploy:** Watchtower auf dem VPS für automatische Container-Updates nach dem Pull-Modell eingerichtet.
- [x] **Integrationstests:** Testskript in Bun / TypeScript für die End-to-End-Prüfung des E-Mail-Versands geschrieben.

---

## 🟡 ETAPPE 1: Vertiefung in Email Security (IN ARBEIT)

Weiterentwicklung der Fachlogik für E-Mail-Sicherheit (Schlüsselkompetenzen für Hornetsecurity):

### 1.1. Kryptografie und Authentizitätsprüfungen
- [ ] **DKIM (DomainKeys Identified Mail):**
  - [ ] Extraktion des `DKIM-Signature`-Headers.
  - [ ] Suche nach dem öffentlichen Schlüssel im DNS (`selector._domainkey.domain.com`).
  - [ ] Prüfung der digitalen Signatur der E-Mail mittels `github.com/toorop/go-dkim`.
- [ ] **DMARC (Domain-based Message Authentication):**
  - [ ] Suche nach der DMARC-Policy der Domain (`_dmarc.domain.com`).
  - [ ] Abgleich der SPF- und DKIM-Ergebnisse mit der DMARC-Policy (`none`, `quarantine`, `reject`).

### 1.2. Bedrohungsanalyse (Threat Intelligence)
- [ ] **Phishing & URL Extractor:**
  - [ ] Extraktion aller `href`-Links aus HTML und Text der E-Mail.
  - [ ] Heuristik: Erkennung von Abweichungen zwischen Anker-Text und tatsächlichem Link (z. B. Text `paypal.com`, aber Link `paypa1-security.com`).
  - [ ] Erkennung direkter IP-Adressen in Links (`http://192.168.1.1/login`).
- [ ] **Attachment Analyzer:**
  - [ ] Berechnung von SHA-256-Hashes für alle Anhänge.
  - [ ] Erkennung gefährlicher doppelter Dateiendungen (z. B. `document.pdf.exe`).
  - [ ] Blockieren gefährlicher MIME-Typen und Dateiendungen (`.exe`, `.scr`, `.bat`, `.vbs`, `.js`, `.iso`).

---

## 🔵 ETAPPE 2: Produktionsreife Architektur und Speicherung

Übergang von In-Memory-Speicherung zu einer verteilten Produktionsarchitektur:

- [ ] **Integration mit Redis (`github.com/redis/go-redis/v9`):**
  - [ ] Implementierung von `RedisStore` als Ersatz für `InMemoryStore`.
  - [ ] Speicherung der E-Mail-Analyseergebnisse mit gesetzter TTL (z. B. 24 Stunden).
  - [ ] Nutzung von Redis als verteilten Cache für SPF/DKIM-Prüfungen.
- [ ] **Strukturiertes Logging (`log/slog`):**
  - [ ] Umstellung aller Logs vom Standardpaket `log` auf `slog`.
  - [ ] Konfiguration der Log-Formatierung im **JSON**-Format zur Kompatibilität mit Grafana Loki / ELK.
- [ ] **Zuverlässigkeit und Timeouts (`context.Context`):**
  - [ ] Hinzufügen von `context.WithTimeout(ctx, 3*time.Second)` für alle DNS- und Netzwerkanfragen.
  - [ ] Schutz der Worker vor „Hängenbleiben" bei langsamer Antwort externer DNS-Server.

---

## 🟣 ETAPPE 3: REST API und Monitoring

Bereitstellung einer externen Schnittstelle für den Zugriff auf Scan-Ergebnisse:

- [ ] **Paralleler HTTP-Server (`net/http`):**
  - [ ] `GET /api/v1/emails` — Abrufen der Liste zuletzt analysierter E-Mails.
  - [ ] `GET /api/v1/emails/{id}` — Abrufen eines detaillierten Sicherheitsberichts zu einer bestimmten E-Mail.
  - [ ] `GET /api/v1/stats` — zusammenfassende Bedrohungsstatistik (Gesamtzahl E-Mails, Spam, SPF/DKIM-Fehlschläge).
  - [ ] `GET /health` — Endpoint für den Servicestatus (Health Check).

---

## 🟠 ETAPPE 4: AI / LLM-Phishing-Analyse

Integration von KI zur intelligenten Bewertung verdächtiger E-Mails (passend zum Stack der Stellenausschreibung):

- [ ] **Modul `ai_analyzer.go`:**
  - [ ] Integration mit der Claude- / OpenAI-API.
  - [ ] Versand verdächtiger E-Mail-Texte zur Analyse auf Social Engineering.
  - [ ] Ermittlung eines abschließenden Risk Score (von 1 bis 10) auf Basis der LLM-Antwort.

---

## ⚪ ETAPPE 5: Codequalität und Testing

- [ ] Erweiterung der Unit-Tests (Abdeckung der Prüflogik für SPF, URLs und Anhänge).
- [ ] Durchführung von Lasttests (Stress-Test mit 1000 versendeten E-Mails über Bun/Go).
- [ ] Einrichtung von `golangci-lint` in GitHub Actions.

---

## 💡 Reihenfolge der aktuellen Aufgaben

1. **Ersatz von `InMemoryStore` durch Redis** + Umstellung der Logs auf **`slog` (JSON)**.
2. **Hinzufügen eines Detektors für gefährliche Links (URL Extractor)** und eines **Anhang-Scanners**.
3. **Implementierung der REST API (`GET /api/v1/emails`)**.
4. **Integration von DKIM / DMARC**.
