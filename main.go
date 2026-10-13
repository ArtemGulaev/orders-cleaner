package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

const defaultSourceURL = "https://test.ooobs.ru/api/orders"

type Order struct {
	ID        json.RawMessage `json:"id"`
	Name      string          `json:"name"`
	Phone     string          `json:"phone"`
	Email     string          `json:"email"`
	Amount    float64         `json:"amount"`
	CreatedAt string          `json:"created_at"`
}

type Client struct {
	Name   string  `json:"name"`
	Phone  string  `json:"phone"`
	Email  string  `json:"email"`
	Amount float64 `json:"amount"`
}

type Stats struct {
	Received         int `json:"received"`
	Unique           int `json:"unique"`
	DuplicatesMerged int `json:"duplicates_merged"`
	InvalidDropped   int `json:"invalid_dropped"`
}

type Response struct {
	Clients []Client `json:"clients"`
	Stats   Stats    `json:"stats"`
}

type Service struct {
	mu     sync.RWMutex
	result Response
	client *http.Client
	source string
}

func NewService(source string) *Service {
	return &Service{
		source: source,
		client: &http.Client{Timeout: 20 * time.Second},
		result: Response{Clients: []Client{}, Stats: Stats{}},
	}
}

func normalizePhone(raw string) (string, bool) {
	var digits strings.Builder
	for _, r := range raw {
		if unicode.IsDigit(r) {
			// Accept only ASCII digits in the normalized Russian phone number.
			if r < '0' || r > '9' {
				return "", false
			}
			digits.WriteRune(r)
		} else if r != '+' && r != '-' && r != '(' && r != ')' && !unicode.IsSpace(r) {
			return "", false
		}
	}
	d := digits.String()
	switch {
	case len(d) == 11 && d[0] == '8':
		d = "7" + d[1:]
	case len(d) == 11 && d[0] == '7':
		// already in country-code form
	case len(d) == 10:
		d = "7" + d
	default:
		return "", false
	}
	return "+" + d, true
}

func parseCreatedAt(s string) time.Time {
	layouts := []string{
		time.RFC3339Nano,
		"2006-01-02 15:04:05",
		"2006-01-02 15:04:05Z07:00",
		"2006-01-02",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, strings.TrimSpace(s)); err == nil {
			return t
		}
	}
	return time.Time{}
}

func transform(orders []Order) Response {
	best := make(map[string]Order)
	invalid := 0
	for _, order := range orders {
		phone, ok := normalizePhone(order.Phone)
		if !ok {
			invalid++
			continue
		}
		order.Phone = phone
		current, exists := best[phone]
		if !exists || order.Amount > current.Amount ||
			(order.Amount == current.Amount && parseCreatedAt(order.CreatedAt).After(parseCreatedAt(current.CreatedAt))) {
			best[phone] = order
		}
	}

	clients := make([]Client, 0, len(best))
	for _, order := range best {
		clients = append(clients, Client{Name: order.Name, Phone: order.Phone, Email: order.Email, Amount: order.Amount})
	}
	sort.Slice(clients, func(i, j int) bool { return clients[i].Phone < clients[j].Phone })
	return Response{
		Clients: clients,
		Stats: Stats{
			Received:         len(orders),
			Unique:           len(clients),
			DuplicatesMerged: len(orders) - invalid - len(clients),
			InvalidDropped:   invalid,
		},
	}
}

func (s *Service) Sync(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.source, nil)
	if err != nil {
		return err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("fetch source orders: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("source API returned HTTP %d", resp.StatusCode)
	}
	var orders []Order
	if err := json.NewDecoder(resp.Body).Decode(&orders); err != nil {
		return fmt.Errorf("decode source orders: %w", err)
	}
	result := transform(orders)
	s.mu.Lock()
	s.result = result // replace the snapshot atomically; repeated syncs never append duplicates
	s.mu.Unlock()
	return nil
}

func (s *Service) snapshot() Response {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.result
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func (s *Service) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/clients", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, s.snapshot())
	})
	mux.HandleFunc("POST /api/sync", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
		defer cancel()
		if err := s.Sync(ctx); err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, s.snapshot())
	})
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	return mux
}

func main() {
	source := strings.TrimSpace(os.Getenv("SOURCE_URL"))
	if source == "" {
		source = defaultSourceURL
	}
	port := strings.TrimSpace(os.Getenv("PORT"))
	if port == "" {
		port = "8080"
	}

	service := NewService(source)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	err := service.Sync(ctx)
	cancel()
	if err != nil {
		// Keep the service available so /api/sync can be retried if the upstream is temporarily down.
		log.Printf("initial sync failed: %v", err)
	} else {
		log.Printf("initial sync complete: %+v", service.snapshot().Stats)
	}

	server := &http.Server{Addr: ":" + port, Handler: service.handler(), ReadHeaderTimeout: 5 * time.Second}
	log.Printf("listening on :%s; source=%s", port, source)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
