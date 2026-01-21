#!/usr/bin/env python3
"""
Simple test script to validate the AIISTECH Backend API
"""
import requests
import json
import sys

BASE_URL = "http://127.0.0.1:8000"


def test_endpoint(name, url, method="GET", data=None):
    """Test an API endpoint"""
    print(f"\n{'='*60}")
    print(f"Testing: {name}")
    print(f"{'='*60}")
    
    try:
        if method == "GET":
            response = requests.get(url)
        elif method == "POST":
            response = requests.post(url, json=data)
        
        print(f"Status Code: {response.status_code}")
        
        if response.status_code == 200:
            result = response.json()
            print(f"Response: {json.dumps(result, indent=2)[:500]}...")
            print("✓ Test PASSED")
            return True
        else:
            print(f"✗ Test FAILED: {response.text}")
            return False
    except Exception as e:
        print(f"✗ Test FAILED with exception: {e}")
        return False


def main():
    """Run all tests"""
    print("AIISTECH Backend API Test Suite")
    print("="*60)
    
    tests = [
        ("Root Endpoint", f"{BASE_URL}/", "GET"),
        ("Health Check", f"{BASE_URL}/health", "GET"),
        ("Design Styles", f"{BASE_URL}/api/design/styles", "GET"),
        ("Color Schemes", f"{BASE_URL}/api/design/color-schemes", "GET"),
        ("Design Generation", f"{BASE_URL}/api/design/generate", "POST", {
            "description": "Test landing page",
            "style": "modern",
            "color_scheme": "light"
        }),
        ("Automation Task Types", f"{BASE_URL}/api/automation/task-types", "GET"),
        ("Automation Task", f"{BASE_URL}/api/automation/task", "POST", {
            "task_type": "generate_component",
            "parameters": {"name": "TestButton", "type": "button"}
        }),
        ("Templates List", f"{BASE_URL}/api/templates/", "GET"),
        ("Template Categories", f"{BASE_URL}/api/templates/categories/list", "GET"),
    ]
    
    passed = 0
    failed = 0
    
    for name, url, method, *rest in tests:
        data = rest[0] if rest else None
        if test_endpoint(name, url, method, data):
            passed += 1
        else:
            failed += 1
    
    print(f"\n{'='*60}")
    print(f"Test Results: {passed} passed, {failed} failed")
    print(f"{'='*60}")
    
    return 0 if failed == 0 else 1


if __name__ == "__main__":
    sys.exit(main())
