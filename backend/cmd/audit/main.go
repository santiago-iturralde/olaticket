package main

import (
	"fmt"
	"log"

	"stock-api/internal/platform/storage/mysql"
)

func main() {
	// 1. CONEXIÓN
	db, err := mysql.NewConnection()
	if err != nil {
		log.Fatal("❌ Error conectando:", err)
	}
	defer db.Close()

	// 2. VERIFICAR EL STOCK FINAL (suma de los sectores del evento 1)
	var stock int
	err = db.QueryRow("SELECT COALESCE(SUM(stock_disponible), 0) FROM sectores WHERE evento_id = 1").Scan(&stock)
	if err != nil {
		log.Fatal("❌ Error leyendo el evento:", err)
	}

	fmt.Printf("\n📊 RESULTADO DE LA AUDITORÍA:\n")
	fmt.Printf("-----------------------------\n")
	fmt.Printf("Stock Final en Base de Datos: %d\n", stock)

	// 3. VEREDICTO
	if stock == 0 {
		fmt.Println("✅ ESTADO: PERFECTO. Se vendió todo, ni uno más, ni uno menos.")
	} else if stock < 0 {
		fmt.Printf("❌ ESTADO: CRÍTICO. Hubo sobreventa (Overbooking). Vendiste %d entradas que no existen.\n", stock*-1)
	} else {
		fmt.Println("⚠️ ESTADO: RARO. Todavía queda stock, ¿el worker terminó de trabajar?")
	}
	fmt.Println("-----------------------------")

	fmt.Println("\n📊 DESGLOSE DE ÓRDENES:")
	rows, err := db.Query("SELECT estado, COUNT(*) FROM ordenes GROUP BY estado")
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()

	for rows.Next() {
		var estado string
		var total int
		rows.Scan(&estado, &total)
		fmt.Printf("➡️  %s: %d\n", estado, total)
	}
	fmt.Println("-----------------------------")
}
