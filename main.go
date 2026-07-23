package main

import (
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jhillyerd/enmime"
	"github.com/mhale/smtpd"
)

// EmailJob представляет задачу в очереди на анализ
type EmailJob struct {
	ID       string    `json:"id"`
	SenderIP string    `json:"sender_ip"`
	From     string    `json:"from"`
	To       []string  `json:"to"`
	RawData  []byte    `json:"-"`
	Received time.Time `json:"received_at"`
}

// AnalysisResult хранит итоговые результаты анализа письма
type AnalysisResult struct {
	JobID           string    `json:"job_id"`
	From            string    `json:"from"`
	SenderIP        string    `json:"sender_ip"`
	To              []string  `json:"to"`
	Subject         string    `json:"subject"`
	SPFValid        bool      `json:"spf_valid"`
	AttachmentCount int       `json:"attachment_count"`
	TextLength      int       `json:"text_length"`
	AnalyzedAt      time.Time `json:"analyzed_at"`
}

// InMemoryStore представляет потокобезопасное хранилище результатов анализа в оперативной памяти
type InMemoryStore struct {
	mu      sync.RWMutex
	results map[string]AnalysisResult
}

// NewInMemoryStore создает новое In-Memory хранилище
func NewInMemoryStore() *InMemoryStore {
	return &InMemoryStore{
		results: make(map[string]AnalysisResult),
	}
}

// Save сохраняет результат анализа письма в память
func (s *InMemoryStore) Save(res AnalysisResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.results[res.JobID] = res
	log.Printf("[InMemoryStore] Сохранен результат анализа ID=%s (Всего элементов в памяти: %d)", res.JobID, len(s.results))
}

// Get получает результат анализа письма по JobID из памяти
func (s *InMemoryStore) Get(jobID string) (AnalysisResult, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	res, ok := s.results[jobID]
	return res, ok
}

// Count возвращает количество сохраненных записей
func (s *InMemoryStore) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.results)
}

// Глобальное in-memory хранилище
var memoryStore = NewInMemoryStore()

/*
// --- Старая реализация Redis (закомментирована по запросу) ---
// import "github.com/redis/go-redis/v9"
// var rdb *redis.Client

// func initRedis() {
// 	redisAddr := os.Getenv("REDIS_ADDR")
// 	if redisAddr == "" {
// 		log.Println("[Redis] Переменная REDIS_ADDR не задана, работа в режиме без кэширования")
// 		return
// 	}
// 	client := redis.NewClient(&redis.Options{
// 		Addr:         redisAddr,
// 		DialTimeout:  3 * time.Second,
// 		ReadTimeout:  2 * time.Second,
// 		WriteTimeout: 2 * time.Second,
// 	})
// 	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
// 	defer cancel()
// 	if err := client.Ping(ctx).Err(); err != nil {
// 		log.Printf("[Redis] Не удалось подключиться к Redis (%s): %v. Работаем без Redis.", redisAddr, err)
// 		return
// 	}
// 	rdb = client
// 	log.Printf("[Redis] Успешное подключение к Redis на %s", redisAddr)
// }

// func saveToRedis(res AnalysisResult) {
// 	if rdb == nil {
// 		return
// 	}
// 	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
// 	defer cancel()
// 	data, err := json.Marshal(res)
// 	if err != nil {
// 		log.Printf("[Redis] Ошибка маршалинга результата ID=%s: %v", res.JobID, err)
// 		return
// 	}
// 	key := fmt.Sprintf("mail:analysis:%s", res.JobID)
// 	if err := rdb.Set(ctx, key, data, 24*time.Hour).Err(); err != nil {
// 		log.Printf("[Redis] Ошибка сохранения в Redis ключ=%s: %v", key, err)
// 	} else {
// 		log.Printf("[Redis] Успешно сохранен результат анализа ID=%s в Redis (TTL=24h)", res.JobID)
// 	}
// }
*/

func main() {
	log.Println("[InMemoryStore] Инициализировано хранилище результатов анализа в оперативной памяти")

	// Инициализируем очередь (канал) для писем
	jobQueue := make(chan EmailJob, 100)

	// Запуск пула воркеров (3 воркера для конкурентной обработки)
	for w := 1; w <= 3; w++ {
		go worker(w, jobQueue)
	}

	// Обработчик входящих писем от библиотеки smtpd
	mailHandler := func(origin net.Addr, from string, to []string, data []byte) error {
		ip := "127.0.0.1"
		if origin != nil {
			ip = strings.Split(origin.String(), ":")[0]
		}

		jobID := fmt.Sprintf("%d", time.Now().UnixNano())
		job := EmailJob{
			ID:       jobID,
			SenderIP: ip,
			From:     from,
			To:       to,
			RawData:  data,
			Received: time.Now(),
		}

		// Передаем задачу в очередь без блокировки сетевого потока
		select {
		case jobQueue <- job:
			log.Printf("[MTA] Письмо ID=%s от %s поставлено в очередь анализа", jobID, from)
		default:
			log.Printf("[MTA] Внимание: Очередь переполнена. Письмо от %s отклонено", from)
			return fmt.Errorf("server busy")
		}
		return nil
	}

	// Настройка и запуск SMTP-сервера (по умолчанию 0.0.0.0:2525)
	listenAddr := os.Getenv("BIND_ADDR")
	if listenAddr == "" {
		listenAddr = "0.0.0.0:2525"
	}

	server := &smtpd.Server{
		Addr:     listenAddr,
		Handler:  mailHandler,
		Appname:  "MailShield_MVP",
		Hostname: "localhost",
	}

	go func() {
		log.Printf("Запуск SMTP-сервера на %s...", listenAddr)
		if err := server.ListenAndServe(); err != nil {
			log.Printf("Завершение SMTP-сервера: %v", err)
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
		log.Printf("[Worker %d] Начало анализа письма ID=%s от %s", id, job.ID, job.From)

		// 1. Парсинг MIME структуры писем
		envelope, err := enmime.ReadEnvelope(strings.NewReader(string(job.RawData)))
		subject := ""
		attachCount := 0
		textLen := 0

		if err != nil {
			log.Printf("[Worker %d] Ошибка парсинга MIME: %v", id, err)
		} else {
			subject = envelope.GetHeader("Subject")
			attachCount = len(envelope.Attachments)
			textLen = len(envelope.Text)
		}

		// 2. Базовый анализ SPF
		spfValid := verifyBasicSPF(job.From, job.SenderIP)

		res := AnalysisResult{
			JobID:           job.ID,
			From:            job.From,
			SenderIP:        job.SenderIP,
			To:              job.To,
			Subject:         subject,
			SPFValid:        spfValid,
			AttachmentCount: attachCount,
			TextLength:      textLen,
			AnalyzedAt:      time.Now(),
		}

		// 3. Вывод результатов анализа
		fmt.Printf("\n===== РЕЗУЛЬТАТ АНАЛИЗА (Воркер %d) =====\n", id)
		fmt.Printf("ID задачи:   %s\n", res.JobID)
		fmt.Printf("Отправитель: %s (IP: %s)\n", res.From, res.SenderIP)
		fmt.Printf("Получатели:  %s\n", strings.Join(res.To, ", "))
		fmt.Printf("Тема письма: %s\n", res.Subject)
		fmt.Printf("Валидность SPF: %t\n", res.SPFValid)
		fmt.Printf("Найдено вложений: %d\n", res.AttachmentCount)
		fmt.Printf("Размер текста: %d символов\n", res.TextLength)
		fmt.Printf("=========================================\n\n")

		// 4. Сохранение результатов в In-Memory хранилище
		memoryStore.Save(res)
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
			if strings.Contains(record, senderIP) || strings.Contains(record, "+all") || strings.Contains(record, "redirect=") {
				return true
			}
			if strings.Contains(record, "all") {
				return true
			}
		}
	}
	return false
}
