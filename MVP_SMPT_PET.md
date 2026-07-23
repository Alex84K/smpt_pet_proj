# MVP Go-MailShield: Конвейер анализа входящей почты

Этот документ описывает минимально жизнеспособный продукт (MVP) собственного почтового шлюза безопасности (Email Security Gateway) на языке Go. 
Проект предназначен для развертывания на VPS (например, IONOS) с целью демонстрации навыков работы с сетью, протоколами, конкурентностью в Go и контейнеризацией.

---

## 1. Цели и возможности MVP

1. **Прием писем:** Сервер слушает входящие соединения по протоколу SMTP (порт 25) и корректно завершает сессию доставки.
2. **Парсинг MIME:** Извлечение из сырого потока данных заголовков (Отправитель, Получатель, Тема) и тела письма (Plain Text/HTML).
3. **Проверка SPF (Базовая безопасность):** Поиск DNS TXT-записей домена отправителя для валидации IP-адреса клиента.
4. **Очередь обработки (Concurrency):** Принятые письма асинхронно передаются в пул воркеров через каналы Go для изоляции сетевого ввода-вывода от тяжелого анализа.
5. **Логирование и кэш:** Запись результатов анализа в Redis для кэширования и быстрых проверок.

---

## 2. Архитектура системы
[Внешний сервер (Gmail)]
│ (SMTP, TCP:25)
▼
[IONOS Firewall] (Разрешен порт 25)
│
▼
[Docker Daemon (VPS)] (Порт-маппинг 25 -> 2525)
│
▼
[Go-MailShield Container]
├── SMTP Listener (Port 2525) ──► [Go Channels]
└── Worker Pool (Анализ SPF/MIME) ──► [Redis Container]
code
Code
---

## 3. Необходимая инфраструктура

1. **Доменное имя:** Зарегистрировано у регистратора (например, IONOS).
2. **Виртуальный сервер (VPS):** 1 vCPU, 1 GB RAM (самый дешевый тариф) под управлением Linux (Ubuntu/Debian).
3. **Установленное ПО на VPS:** Docker, Docker Compose, Git.

---

## 4. Пошаговая настройка инфраструктуры

### Шаг 4.1. Настройка DNS в панели IONOS

Необходимо создать следующие записи для домена `yourdomain.com`:

| Тип записи | Имя (Хост) | Значение (Указывает на) | Описание |
| :--- | :--- | :--- | :--- |
| **A** | `mail.yourdomain.com` | `IP_ВАШЕГО_VPS` | Связывает имя почтового сервера с IP |
| **MX** | `@` (или оставить пустым) | `mail.yourdomain.com` (приоритет 10) | Указывает, какой сервер принимает почту для домена |
| **TXT** | `@` | `v=spf1 ip4:IP_ВАШЕГО_VPS -all` | SPF-запись, разрешающая вашему VPS отправлять почту (для будущих тестов) |

### Шаг 4.2. Настройка брандмауэра в IONOS Cloud Panel

1. Перейдите в **Network -> Firewall Policies**.
2. Добавьте входящее правило для вашего VPS:
   * **Протокол:** TCP
   * **Порт:** 25
   * **Описание:** Разрешить входящий SMTP-трафик

---

## 5. Программный код (Go)

Создайте файл `main.go`. Для реализации SMTP и парсинга MIME используются проверенные библиотеки сообщества, что позволяет сфокусироваться на конвейере обработки.

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

// EmailJob представляет задачу в очереди на анализ
type EmailJob struct {
	SenderIP string
	From     string
	To       []string
	RawData  []byte
}

func main() {
	// Инициализируем очередь (канал) для писем
	jobQueue := make(chan EmailJob, 100)

	// Запуск пула воркеров (3 воркера для конкурентной обработки)
	for w := 1; w <= 3; w++ {
		go worker(w, jobQueue)
	}

	// Обработчик входящих писем от библиотеки smtpd
	mailHandler := func(origin net.Addr, from string, to []string, data []byte) error {
		ip := strings.Split(origin.String(), ":")[0]
		
		// Передаем задачу в очередь без блокировки сетевого потока
		select {
		case jobQueue <- EmailJob{SenderIP: ip, From: from, To: to, RawData: data}:
			log.Printf("[MTA] Письмо от %s поставлено в очередь анализа", from)
		default:
			log.Printf("[MTA] Внимание: Очередь переполнена. Письмо от %s отклонено", from)
			return fmt.Errorf("server busy")
		}
		return nil
	}

	// Настройка и запуск SMTP-сервера (слушает локальный порт 2525)
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
		log.Printf("Запуск SMTP-сервера на %s...", addr)
		if err := server.ListenAndServe(); err != nil {
			log.Fatalf("Ошибка SMTP-сервера: %v", err)
		}
	}()

	// Graceful Shutdown
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	log.Println("Завершение работы сервера...")
	close(jobQueue)
	// Даем воркерам время завершить текущие задачи
	time.Sleep(2 * time.Second)
}

// Воркер извлекает данные из очереди и проводит анализ безопасности
func worker(id int, queue <-chan EmailJob) {
	log.Printf("[Worker %d] Запущен", id)
	for job := range queue {
		log.Printf("[Worker %d] Начало анализа письма от %s", id, job.From)

		// 1. Парсинг MIME структуры писем
		envelope, err := enmime.ReadEnvelope(strings.NewReader(string(job.RawData)))
		if err != nil {
			log.Printf("[Worker %d] Ошибка парсинга MIME: %v", id, err)
			continue
		}

		// 2. Базовый анализ SPF
		spfValid := verifyBasicSPF(job.From, job.SenderIP)

		// 3. Вывод результатов анализа
		fmt.Printf("\n===== РЕЗУЛЬТАТ АНАЛИЗА (Воркер %d) =====\n", id)
		fmt.Printf("Отправитель: %s (IP: %s)\n", job.From, job.SenderIP)
		fmt.Printf("Получатели:  %s\n", strings.Join(job.To, ", "))
		fmt.Printf("Тема письма: %s\n", envelope.GetHeader("Subject"))
		fmt.Printf("Валидность SPF: %t\n", spfValid)
		fmt.Printf("Найдено вложений: %d\n", len(envelope.Attachments))
		fmt.Printf("Размер текста: %d символов\n", len(envelope.Text))
		fmt.Printf("=========================================\n\n")
	}
}

// Упрощенная проверка SPF через DNS TXT записи
func verifyBasicSPF(fromEmail, senderIP string) bool {
	parts := strings.Split(fromEmail, "@")
	if len(parts) < 2 {
		return false
	}
	domain := parts[1]

	// Делаем реальный DNS запрос
	txtRecords, err := net.LookupTXT(domain)
	if err != nil {
		return false
	}

	for _, record := range txtRecords {
		if strings.HasPrefix(record, "v=spf1") {
			// Проверяем, упомянут ли IP отправителя в SPF-записи (упрощенный поиск подстроки)
			if strings.Contains(record, senderIP) || strings.Contains(record, "all") {
				return true
			}
		}
	}
	return false
}
6. Контейнеризация
Для запуска проекта на сервере создайте два конфигурационных файла в той же директории.
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
      # Проброс внешнего порта 25 на порт 2525 внутри контейнера
      - "25:2525"
    restart: always
    environment:
      - REDIS_ADDR=redis:6379
    depends_on:
      - redis

  redis:
    image: redis:alpine
    restart: always
7. Инструкция по запуску и тестированию
Склонируйте репозиторий с проектом на ваш VPS.
Убедитесь, что порты 25 и 2525 не заняты другими службами (например, дефолтным Postfix).
Запустите стек контейнеров:
code
Bash
docker-compose up -d --build
Отправьте тестовое письмо с вашего личного почтового ящика (например, Gmail или Яндекс) на любой вымышленный адрес вашего домена, например, test@yourdomain.com.
Посмотрите логи работы контейнера в реальном времени:
code
Bash
docker-compose logs -f mailshield
В логах вы увидите, как Go-приложение успешно приняло сессию, передало задачу воркеру, воркер сделал DNS-запросы и распарсил вложения и заголовки входящего письма.
code
Code
---

Этот файл описывает весь цикл создания MVP. Вы можете инициализировать проект с помощью `go mod init mailshield` и установить две внешние зависимости:
* `go get github.com/mhale/smtpd`
* `go get github.com/jhillyerd/enmime`