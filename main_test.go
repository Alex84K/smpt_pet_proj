package main

import (
	"testing"
	"time"
)

func TestVerifyBasicSPF_InvalidEmail(t *testing.T) {
	result := verifyBasicSPF("invalid-email-address", "192.168.1.1")
	if result != false {
		t.Errorf("Expected false for invalid email format, got %v", result)
	}
}

func TestVerifyBasicSPF_RealDomain(t *testing.T) {
	result := verifyBasicSPF("test@google.com", "8.8.8.8")
	if !result {
		t.Logf("SPF lookup for google.com returned false (could be DNS environment dependent)")
	}
}

func TestInMemoryStore(t *testing.T) {
	store := NewInMemoryStore()

	res := AnalysisResult{
		JobID:           "job-1",
		From:            "user@domain.com",
		SenderIP:        "1.2.3.4",
		To:              []string{"recipient@domain.com"},
		Subject:         "Test Subject",
		SPFValid:        true,
		AttachmentCount: 1,
		TextLength:      100,
		AnalyzedAt:      time.Now(),
	}

	store.Save(res)

	if store.Count() != 1 {
		t.Errorf("Expected 1 item in store, got %d", store.Count())
	}

	fetched, ok := store.Get("job-1")
	if !ok {
		t.Fatalf("Expected job-1 to exist in store")
	}

	if fetched.Subject != "Test Subject" {
		t.Errorf("Expected subject 'Test Subject', got %s", fetched.Subject)
	}
}

func TestWorkerQueueProcessing(t *testing.T) {
	jobQueue := make(chan EmailJob, 5)

	go worker(99, jobQueue)

	rawMail := []byte("From: sender@example.com\r\nTo: rcpt@example.com\r\nSubject: Test Email\r\n\r\nHello World Body")
	job := EmailJob{
		ID:       "test-job-123",
		SenderIP: "127.0.0.1",
		From:     "sender@example.com",
		To:       []string{"rcpt@example.com"},
		RawData:  rawMail,
		Received: time.Now(),
	}

	jobQueue <- job
	time.Sleep(150 * time.Millisecond)

	close(jobQueue)

	fetched, ok := memoryStore.Get("test-job-123")
	if !ok {
		t.Errorf("Expected test-job-123 to be stored in memoryStore")
	} else if fetched.Subject != "Test Email" {
		t.Errorf("Expected Subject 'Test Email', got '%s'", fetched.Subject)
	}
}
