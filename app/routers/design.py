"""
Design API router
"""
import uuid
from fastapi import APIRouter, HTTPException
from app.models.schemas import DesignRequest, DesignResponse
from app.services.ai_design import ai_design_service

router = APIRouter()


@router.post("/generate", response_model=DesignResponse)
async def generate_design(request: DesignRequest):
    """
    Generate a web design based on the provided specifications
    
    Args:
        request: Design request with specifications
        
    Returns:
        Generated design with HTML and CSS
    """
    try:
        html, css = await ai_design_service.generate_design(request)
        
        design_id = str(uuid.uuid4())
        
        return DesignResponse(
            design_id=design_id,
            html=html,
            css=css,
            description=request.description,
            style=request.style,
            color_scheme=request.color_scheme
        )
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"Error generating design: {str(e)}")


@router.get("/styles")
async def list_styles():
    """List available design styles"""
    return {
        "styles": [
            {"value": "modern", "label": "Modern"},
            {"value": "minimal", "label": "Minimal"},
            {"value": "corporate", "label": "Corporate"},
            {"value": "creative", "label": "Creative"},
            {"value": "elegant", "label": "Elegant"}
        ]
    }


@router.get("/color-schemes")
async def list_color_schemes():
    """List available color schemes"""
    return {
        "color_schemes": [
            {"value": "light", "label": "Light"},
            {"value": "dark", "label": "Dark"},
            {"value": "colorful", "label": "Colorful"},
            {"value": "monochrome", "label": "Monochrome"}
        ]
    }
