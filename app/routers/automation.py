"""
Automation API router
"""
from fastapi import APIRouter, HTTPException
from typing import Optional
from app.models.schemas import AutomationTask, AutomationResponse
from app.services.automation import automation_service

router = APIRouter()


@router.post("/task", response_model=AutomationResponse)
async def create_automation_task(task: AutomationTask):
    """
    Create and execute an automation task
    
    Args:
        task: Automation task specifications
        
    Returns:
        Task execution result
    """
    try:
        result = await automation_service.create_task(
            task_type=task.task_type,
            parameters=task.parameters,
            description=task.description
        )
        
        return AutomationResponse(
            task_id=result["task_id"],
            status=result["status"],
            result=result.get("result"),
            message=f"Task {result['status']}"
        )
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"Error creating task: {str(e)}")


@router.get("/task/{task_id}", response_model=AutomationResponse)
async def get_task_status(task_id: str):
    """
    Get the status of an automation task
    
    Args:
        task_id: Task identifier
        
    Returns:
        Task status and result
    """
    task = automation_service.get_task(task_id)
    if not task:
        raise HTTPException(status_code=404, detail="Task not found")
    
    return AutomationResponse(
        task_id=task["task_id"],
        status=task["status"],
        result=task.get("result"),
        message=f"Task {task['status']}"
    )


@router.get("/tasks")
async def list_tasks():
    """List all automation tasks"""
    tasks = automation_service.list_tasks()
    return {"tasks": tasks, "total": len(tasks)}


@router.get("/task-types")
async def list_task_types():
    """List available automation task types"""
    return {
        "task_types": [
            {
                "type": "generate_component",
                "description": "Generate a web component",
                "parameters": ["name", "type"]
            },
            {
                "type": "optimize_code",
                "description": "Optimize code for better performance",
                "parameters": ["code", "type"]
            },
            {
                "type": "generate_api",
                "description": "Generate API endpoint code",
                "parameters": ["name", "method"]
            },
            {
                "type": "create_workflow",
                "description": "Create an automation workflow",
                "parameters": ["name", "steps"]
            }
        ]
    }
