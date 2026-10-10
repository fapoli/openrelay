import "express";
import { Project } from "../config/env.js";

declare global {
  namespace Express {
    interface Request {
      project?: Project;
    }
  }
}

export {};
