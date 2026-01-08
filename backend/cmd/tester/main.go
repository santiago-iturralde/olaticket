package main

import (
	"bytes"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"sync"
	"time"
)

func main() {
	// --- CONFIGURACIÓN DEL ATAQUE ---
	totalRequests := 2000 // Total de intentos de compra
	concurrency := 100    // Usuarios simultáneos
	targetEventID := 1    // ID 1 = RAC vs Estudiantes

	// URL actualizada (¡Esta es la clave!)
	url := fmt.Sprintf("http://localhost:8080/events/%d/comprar", targetEventID)

	fmt.Println("⚔️  INICIANDO PRUEBA DE CARGA (ATAQUE)")
	fmt.Printf("🎯 Objetivo: RAC vs Estudiantes (ID %d)\n", targetEventID)
	fmt.Printf("🔥 Disparando %d peticiones (%d al mismo tiempo)...\n", totalRequests, concurrency)

	var wg sync.WaitGroup
	results := make(chan string, totalRequests)
	sem := make(chan bool, concurrency) // Semáforo para controlar concurrencia

	start := time.Now()

	for i := 0; i < totalRequests; i++ {
		wg.Add(1)
		sem <- true // Ocupamos un hilo

		go func(id int) {
			defer wg.Done()
			defer func() { <-sem }() // Liberamos hilo

			// Generamos un usuario random
			userID := rand.Intn(90000) + 10000
			jsonBody := []byte(fmt.Sprintf(`{"user_id": %d}`, userID))

			// Hacemos el POST
			resp, err := http.Post(url, "application/json", bytes.NewBuffer(jsonBody))
			if err != nil {
				results <- fmt.Sprintf("Error de Red: %v", err)
				return
			}
			defer resp.Body.Close()

			// Leemos la respuesta por si hay error
			bodyBytes, _ := io.ReadAll(resp.Body)

			if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusAccepted {
				results <- "OK"
			} else {
				// Guardamos el error que nos dio el servidor
				results <- fmt.Sprintf("Fallo HTTP %d: %s", resp.StatusCode, string(bodyBytes))
			}
		}(i)
	}

	wg.Wait()
	close(results)
	duration := time.Since(start)

	// --- REPORTE FINAL ---
	success := 0
	fails := 0
	sampleError := ""

	for res := range results {
		if res == "OK" {
			success++
		} else {
			fails++
			if sampleError == "" {
				sampleError = res
			} // Guardamos un ejemplo
		}
	}

	fmt.Println("\n------------------------------------------------")
	fmt.Printf("🏁 RESULTADOS DEL ATAQUE (%s)\n", duration)
	fmt.Println("------------------------------------------------")
	fmt.Printf("✅ ÉXITOS (Encolados): %d\n", success)
	fmt.Printf("🚫 FALLOS:             %d\n", fails)

	if fails > 0 {
		fmt.Println("\n⚠️  EJEMPLO DE POR QUÉ FALLÓ:")
		fmt.Printf("   %s\n", sampleError)
	}
	fmt.Println("------------------------------------------------")
}
