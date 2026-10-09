import { ZodError, ZodSchema } from "zod";
import { ApiError } from "../errors/ApiError.js";

export function parseBody<T>(schema: ZodSchema<T>, body: unknown): T {
  try {
    return schema.parse(body);
  } catch (err) {
    if (err instanceof ZodError) throw new ApiError(400, err.issues[0].message);
    throw err;
  }
}
