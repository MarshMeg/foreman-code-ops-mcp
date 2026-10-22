// Minimal typings so the fixture type-checks without dependencies.
interface ImportMetaEnv {
  readonly VITE_API_URL: string;
}
interface ImportMeta {
  readonly env: ImportMetaEnv;
}
declare module "axios" {
  type Client = { post<T>(url: string, body: unknown): Promise<{ data: T }> };
  const axios: { create(config: { baseURL: string }): Client };
  export default axios;
}
declare namespace JSX {
  type Element = unknown;
  interface IntrinsicAttributes {
    key?: string | number;
  }
  interface IntrinsicElements {
    [name: string]: Record<string, unknown>;
  }
}
