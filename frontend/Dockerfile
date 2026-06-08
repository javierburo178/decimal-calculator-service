# Build stage
FROM node:20-alpine AS build
WORKDIR /app
COPY package*.json ./
RUN npm ci
COPY . .
# Build with an empty API base so the app calls the relative path /api/calculate,
# which nginx proxies to the backend service (avoids cross-origin calls to :8080).
ENV VITE_API_URL=""
RUN npm run build

# Runtime: static files via nginx
FROM nginx:alpine
COPY --from=build /app/dist /usr/share/nginx/html
COPY nginx.conf /etc/nginx/conf.d/default.conf
EXPOSE 80
