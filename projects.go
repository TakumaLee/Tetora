package main

import "tetora/internal/project"

// Type aliases for backward compatibility with root package callers.
type Project = project.Project
type WorkspaceProjectEntry = project.WorkspaceProjectEntry

func initProjectsDB(dbPath string) error {
	return project.InitDB(dbPath)
}

func listProjects(dbPath, status string) ([]Project, error) {
	return project.List(dbPath, status)
}

func getProject(dbPath, id string) (*Project, error) {
	return project.Get(dbPath, id)
}

func createProject(dbPath string, p Project) error {
	return project.Create(dbPath, p)
}

func updateProject(dbPath string, p Project) error {
	return project.Update(dbPath, p)
}

func deleteProject(dbPath, id string) error {
	return project.Delete(dbPath, id)
}

func parseProjectsMD(path string) ([]WorkspaceProjectEntry, error) {
	return project.ParseProjectsMD(path)
}

func generateProjectID() string {
	return project.GenerateID()
}
