FROM node:22-alpine AS builder

WORKDIR /app

COPY package*.json ./
RUN npm ci

ARG VITE_API_URL=http://localhost:8080
ENV VITE_API_URL=$VITE_API_URL
# Vacío = clave de prueba de Google (ver src/components/Login.jsx)
ARG VITE_RECAPTCHA_SITE_KEY=
ENV VITE_RECAPTCHA_SITE_KEY=$VITE_RECAPTCHA_SITE_KEY

COPY . .
RUN npm run build

FROM nginx:alpine

COPY --from=builder /app/dist /usr/share/nginx/html
COPY nginx.conf /etc/nginx/conf.d/default.conf

EXPOSE 80
