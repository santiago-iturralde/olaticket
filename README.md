# 🎫 Olaticket - Motor de Venta de Entradas de Alta Frecuencia

> **Sistema de venta de tickets resistente a picos de tráfico masivo (High Concurrency), diseñado para garantizar la integridad del stock en tiempo real.**

![Project Status](https://img.shields.io/badge/Status-Development-green)
![Tech Stack](https://img.shields.io/badge/Stack-Go%20%7C%20Redis%20%7C%20React%20%7C%20MySQL-blue)

## 📖 El Problema (The Challenge)
En eventos de alta demanda (partidos de fútbol, conciertos masivos), los sistemas tradicionales fallan por dos razones:
1.  **Race Conditions:** Múltiples usuarios compran el último ticket al mismo milisegundo, generando sobreventa.
2.  **Database Lock:** La base de datos colapsa al intentar escribir miles de transacciones simultáneas.

## 🚀 La Solución (Architecture)
**Olaticket** implementa una arquitectura desacoplada utilizando **Golang** para el procesamiento concurrente y **Redis** como buffer de alta velocidad.

### Principales Características:
* **Fila Virtual (Fair Queueing):** Sistema de encolamiento para proteger al backend de saturación.
* **Atomicidad:** Uso de transacciones y *mutex* para asegurar que 1 ticket = 1 usuario.
* **Feedback en Tiempo Real:** Interfaz optimizada que maneja estados de "Agotado" sin recargar la página.
* **Soporte de Carga:** Probado con tests de estrés de +2000 request/segundo.

## 🛠️ Stack Tecnológico

| Componente | Tecnología | Razón de la elección |
|------------|------------|-----------------------|
| **Backend** | **Golang (Gin)** | Manejo nativo de concurrencia (Goroutines) y baja latencia. |
| **Cache/Lock**| **Redis** | Gestión de stock en memoria y prevención de Race Conditions. |
| **Database** | **MySQL** | Persistencia relacional para el registro final de órdenes. |
| **Frontend** | **React + Vite** | SPA rápida con feedback visual inmediato (Optimistic UI). |
| **Infra** | **Docker** | Contenerización para despliegue reproducible. |

## ⚡ Cómo correr el proyecto

### Prerrequisitos
* Docker & Docker Compose
* Go 1.22+ (Opcional si usas Docker)
* Node.js 18+

### Instalación Rápida
1.  **Clonar el repositorio**
    ```bash
    git clone https://github.com/santiago-iturralde/olaticket.git
cd olaticket
    ```

2.  **Levantar servicios (MySQL + Redis)**
    ```bash
    docker-compose up -d
    ```

3.  **Iniciar Backend**
    ```bash
    cd backend
    go run cmd/api/main.go
    ```

4.  **Iniciar Frontend**
    ```bash
    cd frontend
    npm install
    npm run dev
    ```

## 🧪 Pruebas de Carga (Stress Testing)
El sistema incluye un script de **"Ataque Simulado"** para verificar la integridad del stock.

**Resultado del último test:**
* **Escenario:** 2000 usuarios concurrentes intentando comprar 150 entradas.
* **Resultado:** 150 ventas exitosas, 1850 rechazos controlados. **0 Sobreventas.**
* **Tiempo de respuesta promedio:** <20ms.

Para correr el test:
```bash
go run cmd/tester/main.go

```

📸 Screenshots
Home con Grid Responsiva
<img width="1919" height="958" alt="1" src="https://github.com/user-attachments/assets/746adf0d-fede-4a87-9d9c-5ee286bd57c1" />

Fila virtual
![image](https://github.com/user-attachments/assets/e18b5ae1-b3fe-4aab-814e-a622bbf5b1fe)

Ticket QR Generado
