"""
Data models for the application
"""
from pydantic import BaseModel, Field
from typing import Optional, List, Dict, Any
from enum import Enum


class DesignStyle(str, Enum):
    """Design style options"""
    MODERN = "modern"
    MINIMAL = "minimal"
    CORPORATE = "corporate"
    CREATIVE = "creative"
    ELEGANT = "elegant"


class ColorScheme(str, Enum):
    """Color scheme options"""
    LIGHT = "light"
    DARK = "dark"
    COLORFUL = "colorful"
    MONOCHROME = "monochrome"


class DesignRequest(BaseModel):
    """Request model for design generation"""
    description: str = Field(..., description="Description of the desired design")
    style: DesignStyle = Field(default=DesignStyle.MODERN, description="Design style")
    color_scheme: ColorScheme = Field(default=ColorScheme.LIGHT, description="Color scheme")
    components: Optional[List[str]] = Field(default=None, description="Required components")
    additional_requirements: Optional[str] = Field(default=None, description="Additional requirements")


class DesignResponse(BaseModel):
    """Response model for design generation"""
    design_id: str = Field(..., description="Unique design identifier")
    html: str = Field(..., description="Generated HTML code")
    css: str = Field(..., description="Generated CSS code")
    description: str = Field(..., description="Design description")
    style: DesignStyle
    color_scheme: ColorScheme


class AutomationTask(BaseModel):
    """Automation task model"""
    task_type: str = Field(..., description="Type of automation task")
    parameters: Dict[str, Any] = Field(default={}, description="Task parameters")
    description: Optional[str] = Field(default=None, description="Task description")


class AutomationResponse(BaseModel):
    """Response model for automation tasks"""
    task_id: str = Field(..., description="Unique task identifier")
    status: str = Field(..., description="Task status")
    result: Optional[Dict[str, Any]] = Field(default=None, description="Task result")
    message: str = Field(..., description="Status message")


class Template(BaseModel):
    """Template model"""
    template_id: str = Field(..., description="Unique template identifier")
    name: str = Field(..., description="Template name")
    description: str = Field(..., description="Template description")
    category: str = Field(..., description="Template category")
    html: str = Field(..., description="HTML template")
    css: str = Field(..., description="CSS template")
    preview_url: Optional[str] = Field(default=None, description="Preview URL")


class TemplateListResponse(BaseModel):
    """Response model for template listing"""
    templates: List[Template] = Field(..., description="List of templates")
    total: int = Field(..., description="Total number of templates")
