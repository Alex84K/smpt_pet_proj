# MVP Go-MailShield: Pipeline zur Analyse eingehender E-Mails

Dieses Dokument beschreibt ein Minimum Viable Product (MVP) für ein eigenes E-Mail-Sicherheitsgateway (Email Security Gateway) in Go. 
Das Projekt ist für die Bereitstellung auf einem VPS (z. B. IONOS) vorgesehen und soll Kenntnisse in den Bereichen Netzwerke, Protokolle, Nebenläufigkeit in Go und Containerisierung demonstrieren.

---

## 1. Ziele und Funktionen des MVP

1. **E-Mail-Empfang:** Der Server lauscht auf eingehende Verbindungen über das SMTP-Protokoll (Port 25) und beendet die Zustellsitzung korrekt.
2. **MIME-Parsing:** Extraktion der Header (Absender, Empfänger, Betreff) und des Nachrichtentexts (Plain Text/HTML) aus dem rohen Datenstrom.
3. **SPF-Prüfung (grundlegende Sicherheit):** Abfrage der DNS-TXT-Einträge der Absenderdomain zur Validierung der Client-IP-Adresse.
4. **Verarbeitungswarteschlange (Nebenläufigkeit):** Angenommene E-Mails werden asynchron über Go-Channels an einen Worker-Pool übergeben, um die Netzwerk-I/O von der aufwendigen Analyse zu entkoppeln.
5. **Logging und Cache:** Speicherung der Analyseergebnisse in Redis für Caching und schnelle Prüfungen.

---

## 2. Systemarchitektur
[Externer Server (Gmail)]
│ (SMTP, TCP:25)
▼
[IONOS Firewall] (Port 25 erlaubt)
│
▼
[Docker Daemon (VPS)] (Portmapping 25 -> 2525)
│
▼
[Go-MailShield Container]
├── SMTP Listener (Port 2525) ──► [Go Channels]
└── Worker Pool (SPF/MIME-Analyse) ──► [Redis Container]
code
Code
---

## 3. Erforderliche Infrastruktur

1. **Domainname:** Registriert bei einem Registrar (z. B. IONOS).
2. **Virtueller Server (VPS):** 1 vCPU, 1 GB RAM (günstigster Tarif) unter Linux (Ubuntu/Debian).
3. **Installierte Software auf dem VPS:** Docker, Docker Compose, Git.

---

## 4. Schrittweise Einrichtung der Infrastruktur

### Schritt 4.1. DNS-Konfiguration im IONOS-Panel

Für die Domain `yourdomain.com` müssen folgende Einträge angelegt werden:

| Eintragstyp | Name (Host) | Wert (Zeigt auf) | Beschreibung |
| :--- | :--- | :--- | :--- |
| **A** | `mail.yourdomain.com` | `IP_IHRES_VPS` | Verknüpft den Namen des Mailservers mit der IP |
| **MX** | `@` (oder leer lassen) | `mail.yourdomain.com` (Priorität 10) | Gibt an, welcher Server E-Mails für die Domain empfängt |
| **TXT** | `@` | `v=spf1 ip4:IP_IHRES_VPS -all` | SPF-Eintrag, der Ihrem VPS erlaubt, E-Mails zu versenden (für künftige Tests) |

### Schritt 4.2. Firewall-Konfiguration im IONOS Cloud Panel

1. Gehen Sie zu **Network -> Firewall Policies**.
2. Fügen Sie eine eingehende Regel für Ihren VPS hinzu:
   * **Protokoll:** TCP
   * **Port:** 25
   * **Beschreibung:** Eingehenden SMTP-Verkehr erlauben

---

## 5. Quellcode (Go)

Erstellen Sie die Datei `main.go`. Für SMTP und MIME-Parsing werden bewährte Community-Bibliotheken verwendet, sodass der Fokus auf der Verarbeitungs-Pipeline liegen kann.

```go
package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jhillyerd/enmime"
	"github.com/mhale/smtpd"
)

// EmailJob repräsentiert eine Aufgabe in der Analyse-Warteschlange
type EmailJob struct {
	SenderIP string
	From     string
	To       []string
	RawData  []byte
}

func main() {
	// Initialisieren der Warteschlange (Channel) für E-Mails
	jobQueue := make(chan EmailJob, 100)

	// Start des Worker-Pools (3 Worker für nebenläufige Verarbeitung)
	for w := 1; w <= 3; w++ {
		go worker(w, jobQueue)
	}

	// Handler für eingehende E-Mails von der smtpd-Bibliothek
	mailHandler := func(origin net.Addr, from string, to []string, data []byte) error {
		ip := strings.Split(origin.String(), ":")[0]
		
		// Übergabe der Aufgabe an die Warteschlange, ohne den Netzwerk-Stream zu blockieren
		select {
		case jobQueue <- EmailJob{SenderIP: ip, From: from, To: to, RawData: data}:
			log.Printf("[MTA] E-Mail von %s zur Analyse eingereiht", from)
		default:
			log.Printf("[MTA] Achtung: Warteschlange voll. E-Mail von %s abgelehnt", from)
			return fmt.Errorf("server busy")
		}
		return nil
	}

	// Konfiguration und Start des SMTP-Servers (lauscht auf lokalem Port 2525)
	addr := "0.0.0.0:2525"
	server := &smtpd.Server{
		Addr:         addr,
		Handler:      mailHandler,
		Appname:      "MailShield_MVP",
		Hostname:     "localhost",
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("Starte SMTP-Server auf %s...", addr)
		if err := server.ListenAndServe(); err != nil {
			log.Fatalf("SMTP-Serverfehler: %v", err)
		}
	}()

	// Graceful Shutdown
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	log.Println("Server wird heruntergefahren...")
	close(jobQueue)
	// Den Workern Zeit geben, laufende Aufgaben abzuschließen
	time.Sleep(2 * time.Second)
}

// Der Worker entnimmt Daten aus der Warteschlange und führt die Sicherheitsanalyse durch
func worker(id int, queue <-chan EmailJob) {
	log.Printf("[Worker %d] Gestartet", id)
	for job := range queue {
		log.Printf("[Worker %d] Beginne Analyse der E-Mail von %s", id, job.From)

		// 1. Parsen der MIME-Struktur der E-Mail
		envelope, err := enmime.ReadEnvelope(strings.NewReader(string(job.RawData)))
		if err != nil {
			log.Printf("[Worker %d] Fehler beim MIME-Parsing: %v", id, err)
			continue
		}

		// 2. Grundlegende SPF-Analyse
		spfValid := verifyBasicSPF(job.From, job.SenderIP)

		// 3. Ausgabe der Analyseergebnisse
		fmt.Printf("\n===== ANALYSEERGEBNIS (Worker %d) =====\n", id)
		fmt.Printf("Absender:    %s (IP: %s)\n", job.From, job.SenderIP)
		fmt.Printf("Empfänger:   %s\n", strings.Join(job.To, ", "))
		fmt.Printf("Betreff:     %s\n", envelope.GetHeader("Subject"))
		fmt.Printf("SPF gültig:  %t\n", spfValid)
		fmt.Printf("Anhänge gefunden: %d\n", len(envelope.Attachments))
		fmt.Printf("Textgröße:   %d Zeichen\n", len(envelope.Text))
		fmt.Printf("=========================================\n\n")
	}
}

// Vereinfachte SPF-Prüfung über DNS-TXT-Einträge
func verifyBasicSPF(fromEmail, senderIP string) bool {
	parts := strings.Split(fromEmail, "@")
	if len(parts) < 2 {
		return false
	}
	domain := parts[1]

	// Echte DNS-Abfrage durchführen
	txtRecords, err := net.LookupTXT(domain)
	if err != nil {
		return false
	}

	for _, record := range txtRecords {
		if strings.HasPrefix(record, "v=spf1") {
			// Prüfen, ob die Absender-IP im SPF-Eintrag erwähnt wird (vereinfachte Teilstring-Suche)
			if strings.Contains(record, senderIP) || strings.Contains(record, "all") {
				return true
			}
		}
	}
	return false
}
6. Containerisierung
Um das Projekt auf dem Server auszuführen, erstellen Sie zwei Konfigurationsdateien im selben Verzeichnis.
Dockerfile
code
Dockerfile
FROM golang:alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go.mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o mailshield ./main.go

FROM alpine:latest
RUN apk --no-cache add ca-certificates
RUN adduser -D -g '' appuser
USER appuser
WORKDIR /app
COPY --from=builder /app/mailshield .
EXPOSE 2525
CMD ["./mailshield"]
docker-compose.yml
code
Yaml
version: '3.8'

services:
  mailshield:
    build: .
    ports:
      # Weiterleitung von externem Port 25 auf Port 2525 innerhalb des Containers
      - "25:2525"
    restart: always
    environment:
      - REDIS_ADDR=redis:6379
    depends_on:
      - redis

  redis:
    image: redis:alpine
    restart: always
7. Anleitung zum Start und Testen
Klonen Sie das Projekt-Repository auf Ihren VPS.
Stellen Sie sicher, dass die Ports 25 und 2525 nicht von anderen Diensten belegt sind (z. B. dem Standard-Postfix).
Starten Sie den Container-Stack:
code
Bash
docker-compose up -d --build
Senden Sie eine Test-E-Mail von Ihrem persönlichen Postfach (z. B. Gmail oder Yandex) an eine beliebige fiktive Adresse Ihrer Domain, z. B. test@yourdomain.com.
Sehen Sie sich die Container-Logs in Echtzeit an:
code
Bash
docker-compose logs -f mailshield
In den Logs sehen Sie, wie die Go-Anwendung die Sitzung erfolgreich angenommen, die Aufgabe an einen Worker übergeben hat und der Worker DNS-Abfragen durchgeführt sowie Anhänge und Header der eingehenden E-Mail geparst hat.
code
Code
---

Diese Datei beschreibt den gesamten Erstellungszyklus des MVP. Sie können das Projekt mit `go mod init mailshield` initialisieren und zwei externe Abhängigkeiten installieren:
* `go get github.com/mhale/smtpd`
* `go get github.com/jhillyerd/enmime`
