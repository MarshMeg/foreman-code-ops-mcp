// Minimal JSX typing so the fixture type-checks without React.
declare namespace JSX {
  type Element = unknown;
  interface IntrinsicAttributes {
    key?: string | number;
  }
  interface IntrinsicElements {
    [name: string]: Record<string, unknown>;
  }
}
