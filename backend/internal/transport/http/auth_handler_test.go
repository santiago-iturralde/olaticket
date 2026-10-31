package http

import (
	"errors"
	"testing"
	"time"
)

func validRegisterRequest() registerRequest {
	return registerRequest{
		Email:           "  Ana@Example.com ",
		Password:        "Secreta123",
		Nombre:          " Ana ",
		Apellido:        "Gómez",
		PaisResidencia:  "Argentina",
		Telefono:        "+54  2284 123456",
		TipoDocumento:   "dni",
		NumeroDNI:       "30.123.456",
		FechaNacimiento: "1995-04-12",
	}
}

func TestRegisterRequestNormalizes(t *testing.T) {
	user, err := validRegisterRequest().toUser(time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("toUser() error = %v", err)
	}
	if user.Email != "ana@example.com" || user.Nombre != "Ana" || user.Telefono != "+54 2284 123456" ||
		user.TipoDocumento != "DNI" || user.NumeroDNI != "30123456" {
		t.Fatalf("toUser() = %+v", user)
	}
}

func TestRegisterRequestRejectsInvalidFields(t *testing.T) {
	now := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	cases := map[string]func(*registerRequest){
		"password":         func(r *registerRequest) { r.Password = "secreta123" },
		"nombre":           func(r *registerRequest) { r.Nombre = "   " },
		"telefono":         func(r *registerRequest) { r.Telefono = "2284123456" },
		"tipo_documento":   func(r *registerRequest) { r.TipoDocumento = "LIBRETA" },
		"numero_dni":       func(r *registerRequest) { r.NumeroDNI = "123" },
		"fecha_nacimiento": func(r *registerRequest) { r.FechaNacimiento = "2030-01-01" },
		"email":            func(r *registerRequest) { r.Email = "no-es-un-email" },
	}
	for field, mutate := range cases {
		req := validRegisterRequest()
		mutate(&req)
		_, err := req.toUser(now)
		var fe *fieldError
		if !errors.As(err, &fe) || fe.Field != field {
			t.Errorf("campo %s: toUser() error = %v", field, err)
		}
	}
}

func TestValidatePassword(t *testing.T) {
	for password, ok := range map[string]bool{
		"Abcdefg1": true,
		"Abcdef1":  false, // 7 caracteres
		"abcdefg1": false, // sin mayúscula
		"Abcdefgh": false, // sin número
	} {
		if err := validatePassword(password); (err == nil) != ok {
			t.Errorf("validatePassword(%q) error = %v", password, err)
		}
	}
}
