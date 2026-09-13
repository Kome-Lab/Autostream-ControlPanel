

export function asError(error: unknown) {
  return error instanceof Error ? error : new Error(String(error));
}

export function delay(milliseconds: number) {
  return new Promise<void>((resolveDelay) => setTimeout(resolveDelay, milliseconds));
}

export async function withTimeout<T>(promise: Promise<T>, milliseconds: number, message: string) {
  let timer: ReturnType<typeof setTimeout> | undefined;
  try {
    return await Promise.race([
      promise,
      new Promise<never>((_, rejectTimeout) => {
        timer = setTimeout(() => rejectTimeout(new Error(message)), milliseconds);
      }),
    ]);
  } finally {
    if (timer) clearTimeout(timer);
  }
}
