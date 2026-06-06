/// <reference types="vite/client" />

interface ImportMetaEnv {
  /** Base URL of the calculator backend. Defaults to http://localhost:8080. */
  readonly VITE_API_URL?: string
}

interface ImportMeta {
  readonly env: ImportMetaEnv
}
