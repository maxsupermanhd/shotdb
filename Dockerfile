FROM golang:1.25 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN ./build.sh

FROM nginx:alpine
COPY --from=build /src/dist /usr/share/nginx/html
