"""
AIISTECH Backend - AI Web Design & Automated Systems
Main application entry point
"""
import os
from fastapi import FastAPI, HTTPException
from fastapi.middleware.cors import CORSMiddleware
from dotenv import load_dotenv
import uvicorn

from app.routers import design, automation, templates
from app.config import settings

# Load environment variables
load_dotenv()

# Create FastAPI application
app = FastAPI(
    title="AIISTECH Backend",
    description="AI-powered web design and automated systems backend",
    version="1.0.0"
)

# Configure CORS
app.add_middleware(
    CORSMiddleware,
    allow_origins=["*"],  # Configure appropriately for production
    allow_credentials=True,
    allow_methods=["*"],
    allow_headers=["*"],
)

# Include routers
app.include_router(design.router, prefix="/api/design", tags=["Design"])
app.include_router(automation.router, prefix="/api/automation", tags=["Automation"])
app.include_router(templates.router, prefix="/api/templates", tags=["Templates"])


@app.get("/")
async def root():
    """Root endpoint with API information"""
    return {
        "message": "AIISTECH Backend API",
        "version": "1.0.0",
        "description": "AI Web Design & Automated Systems",
        "endpoints": {
            "design": "/api/design",
            "automation": "/api/automation",
            "templates": "/api/templates"
        }
    }


@app.get("/health")
async def health_check():
    """Health check endpoint"""
    return {"status": "healthy", "service": "AIISTECH Backend"}


if __name__ == "__main__":
    uvicorn.run(
        "main:app",
        host=settings.HOST,
        port=settings.PORT,
        reload=settings.DEBUG
    )
