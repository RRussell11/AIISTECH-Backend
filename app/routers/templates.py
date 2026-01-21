"""
Templates API router
"""
from fastapi import APIRouter, HTTPException, Query
from typing import Optional
from app.models.schemas import Template, TemplateListResponse
from app.services.templates import template_service

router = APIRouter()


@router.get("/", response_model=TemplateListResponse)
async def list_templates(category: Optional[str] = Query(None, description="Filter by category")):
    """
    List all available templates
    
    Args:
        category: Optional category filter
        
    Returns:
        List of templates
    """
    templates = template_service.list_templates(category=category)
    return TemplateListResponse(
        templates=[Template(**t) for t in templates],
        total=len(templates)
    )


@router.get("/{template_id}", response_model=Template)
async def get_template(template_id: str):
    """
    Get a specific template by ID
    
    Args:
        template_id: Template identifier
        
    Returns:
        Template details
    """
    template = template_service.get_template(template_id)
    if not template:
        raise HTTPException(status_code=404, detail="Template not found")
    
    return Template(**template)


@router.get("/categories/list")
async def list_categories():
    """List all template categories"""
    templates = template_service.list_templates()
    categories = list(set(t.get("category") for t in templates))
    
    return {
        "categories": [
            {"value": cat, "label": cat.title()}
            for cat in categories
        ]
    }
