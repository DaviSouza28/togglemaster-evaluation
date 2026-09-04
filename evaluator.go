package main

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	CACHE_TTL    = 30 * time.Second
	maxBodyBytes = 1 << 20 // 1 MiB
)

// getDecision e o wrapper principal.
func (a *App) getDecision(userID, flagName string) (bool, error) {
	info, err := a.getCombinedFlagInfo(flagName)
	if err != nil {
		return false, err
	}

	return a.runEvaluationLogic(info, userID), nil
}

// getCombinedFlagInfo busca os dados no Redis, com fallback para os microsservicos.
func (a *App) getCombinedFlagInfo(flagName string) (*CombinedFlagInfo, error) {
	cacheKey := fmt.Sprintf("flag_info:%s", flagName)

	val, err := a.RedisClient.Get(ctx, cacheKey).Result()
	if err == nil {
		var info CombinedFlagInfo
		if err := json.Unmarshal([]byte(val), &info); err == nil {
			log.Println("Cache HIT")
			return &info, nil
		} else {
			log.Printf("Erro ao desserializar cache: %v", err)
		}
	}

	log.Println("Cache MISS")

	info, err := a.fetchFromServices(flagName)
	if err != nil {
		return nil, err
	}

	jsonData, err := json.Marshal(info)
	if err != nil {
		log.Printf("Erro ao serializar dados para cache: %v", err)
		return info, nil
	}

	if err := a.RedisClient.Set(ctx, cacheKey, jsonData, CACHE_TTL).Err(); err != nil {
		log.Printf("Erro ao atualizar cache Redis: %v", err)
	}

	return info, nil
}

// fetchFromServices busca dados do flag-service e targeting-service concorrentemente.
func (a *App) fetchFromServices(flagName string) (*CombinedFlagInfo, error) {
	var wg sync.WaitGroup
	wg.Add(2)

	var flagInfo *Flag
	var ruleInfo *TargetingRule
	var flagErr, ruleErr error

	go func() {
		defer wg.Done()
		flagInfo, flagErr = a.fetchFlag(flagName)
	}()

	go func() {
		defer wg.Done()
		ruleInfo, ruleErr = a.fetchRule(flagName)
	}()

	wg.Wait()

	if flagErr != nil {
		return nil, flagErr
	}

	if ruleErr != nil {
		log.Println("Aviso: nenhuma regra de segmentacao encontrada. Usando padrao.")
	}

	return &CombinedFlagInfo{
		Flag: flagInfo,
		Rule: ruleInfo,
	}, nil
}

// buildServiceURL cria uma URL somente a partir de um endpoint base confiavel
// configurado pela aplicacao. O valor externo e usado apenas como segmento do path.
func buildServiceURL(baseURL, resource, flagName string) (string, error) {
	base, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("URL base invalida: %w", err)
	}

	if base.Scheme != "http" && base.Scheme != "https" {
		return "", fmt.Errorf("protocolo da URL base nao permitido")
	}

	if base.Host == "" {
		return "", fmt.Errorf("host da URL base nao pode ser vazio")
	}

	base.RawQuery = ""
	base.Fragment = ""

	base.Path = strings.TrimRight(base.Path, "/") +
		"/" + resource + "/" + url.PathEscape(flagName)

	return base.String(), nil
}

func (a *App) fetchFlag(flagName string) (*Flag, error) {
	serviceURL, err := buildServiceURL(a.FlagServiceURL, "flags", flagName)
	if err != nil {
		return nil, fmt.Errorf("erro ao construir URL do flag-service: %w", err)
	}

	apiKey := os.Getenv("SERVICE_API_KEY")

	req, err := http.NewRequest(http.MethodGet, serviceURL, nil)
	if err != nil {
		return nil, fmt.Errorf("erro ao criar requisicao para flag-service: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := a.HttpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("erro ao chamar flag-service: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.Printf("Erro ao fechar resposta HTTP: %v", err)
		}
	}()

	if resp.StatusCode == http.StatusNotFound {
		return nil, &NotFoundError{flagName}
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("flag-service retornou status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("erro ao ler resposta do flag-service: %w", err)
	}

	var flag Flag
	if err := json.Unmarshal(body, &flag); err != nil {
		return nil, fmt.Errorf("erro ao desserializar resposta do flag-service: %w", err)
	}

	return &flag, nil
}

func (a *App) fetchRule(flagName string) (*TargetingRule, error) {
	serviceURL, err := buildServiceURL(a.TargetingServiceURL, "rules", flagName)
	if err != nil {
		return nil, fmt.Errorf("erro ao construir URL do targeting-service: %w", err)
	}

	apiKey := os.Getenv("SERVICE_API_KEY")

	req, err := http.NewRequest(http.MethodGet, serviceURL, nil)
	if err != nil {
		return nil, fmt.Errorf("erro ao criar requisicao para targeting-service: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := a.HttpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("erro ao chamar targeting-service: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.Printf("Erro ao fechar resposta HTTP: %v", err)
		}
	}()

	if resp.StatusCode == http.StatusNotFound {
		return nil, &NotFoundError{flagName}
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("targeting-service retornou status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("erro ao ler resposta do targeting-service: %w", err)
	}

	var rule TargetingRule
	if err := json.Unmarshal(body, &rule); err != nil {
		return nil, fmt.Errorf("erro ao desserializar resposta do targeting-service: %w", err)
	}

	return &rule, nil
}

func (a *App) runEvaluationLogic(info *CombinedFlagInfo, userID string) bool {
	if info.Flag == nil || !info.Flag.IsEnabled {
		return false
	}

	if info.Rule == nil || !info.Rule.IsEnabled {
		return true
	}

	rule := info.Rule.Rules

	if rule.Type == "PERCENTAGE" {
		percentage, ok := rule.Value.(float64)
		if !ok {
			log.Println("Erro: valor da regra de porcentagem nao e um numero")
			return false
		}

		userBucket := getDeterministicBucket(userID + info.Flag.Name)

		if float64(userBucket) < percentage {
			return true
		}
	}

	return false
}

func getDeterministicBucket(input string) int {
	hash := sha256.Sum256([]byte(input))

	val := binary.BigEndian.Uint32(hash[:4])

	return int(val % 100)
}
