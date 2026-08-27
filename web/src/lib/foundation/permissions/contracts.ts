export type NonEmptyReadonlyArray<T> = readonly [T, ...T[]];

export type PermissionRequirement =
  | {
      kind: "all";
      permissions: NonEmptyReadonlyArray<string>;
    }
  | {
      kind: "any";
      permissions: NonEmptyReadonlyArray<string>;
    }
  | {
      kind: "none";
      permissions?: never;
    };
