import "express";
import { Project } from "../config.js";

declare global {
  namespace Express {
    interface Request {
      project?: Project;
    }
  }
}

export {};
