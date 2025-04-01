# Visual Novel Generation Server

This Go server provides an API for generating visual novel configurations and content using the DeepSeek AI model.

## Features

-   Generates initial novel configuration based on user prompts (using `promts/narrator.md`).
-   Generates novel content (scenes, characters, choices) step-by-step (using `promts/novel_creator.md`).
-   Supports state management for ongoing novel sessions using User IDs.
-   Provides API endpoints for interaction.
-   Connects to a PostgreSQL database for potential future state persistence.

## Prerequisites

-   Go (version 1.21+ recommended)
-   DeepSeek API Key
-   Access to a running PostgreSQL database

## Installation

1.  Clone the repository:
    ```bash
    git clone <repository_url>
    cd novel-server
    ```
2.  Install dependencies:
    ```bash
    go mod tidy
    ```

## Configuration

Create a `config.yaml` file in the root directory (or use environment variables). See `config.example.yaml` for structure.

Key configuration options:

-   `deepseek.api_key`: Your DeepSeek API key.
-   `deepseek.model_name`: The DeepSeek model to use (e.g., `deepseek-chat`).
-   `server.host`: Host for the server (default: `localhost`).
-   `server.port`: Port for the server (default: `8080`).
-   `api.base_path`: Base path for API endpoints (default: `/api`).

**Database Configuration (Environment Variables):**

The database connection is configured using environment variables:

-   `DATABASE_HOST`: Hostname of your PostgreSQL server (default: `localhost`).
-   `DATABASE_PORT`: Port of your PostgreSQL server (default: `5432`).
-   `DATABASE_USER`: Username for the database.
-   `DATABASE_PASSWORD`: Password for the database user.
-   `DATABASE_NAME`: Name of the database to connect to.

Make sure these environment variables are set before running the server.

## Running the Server

1.  Set the required environment variables (DeepSeek API key and Database credentials).
2.  Run the server:
    ```bash
    go run cmd/server/main.go
    ```

The server will start on the configured host and port (e.g., `localhost:8080`).

## API Endpoints

-   `POST /api/generate-novel`: Generates the initial novel configuration.
    -   Request Body: `{ "user_prompt": "Your novel idea..." }`
    -   Response Body: `{ "config": { ...NovelConfig... } }`
-   `POST /api/generate-novel-content`: Generates novel setup or the next scene.
    -   Request Body (Initial): `{ "user_id": "some_user", "config": { ...NovelConfig... } }`
    -   Request Body (Continuation): `{ "user_id": "some_user", "state": { ...NovelState... }, "user_choice": { ...UserChoice... } }` (user_choice is optional)
    -   Response Body: `{ "state": { ...NovelState... }, "new_content": { ...SetupContent or SceneContent... } }`

## Client Example

A basic Node.js client example is available in the `novel-client` directory. See `novel-client/README.md` (if it exists) or the script itself (`novel-client/index.js`) for usage instructions.

## TODO

-   Implement actual database persistence for `NovelState` using the `dbPool`.
-   Add more robust error handling.
-   Add unit and integration tests. 