// Copyright (c) 2025 Ehab Terra
// SPDX-License-Identifier: MIT

package yaml_test

import (
	"database/sql"
	"testing"

	"github.com/ehabterra/workflow/yaml"
	_ "github.com/mattn/go-sqlite3"
)

func TestSQLConnection_DB(t *testing.T) {
	// Create SQLConnection through SetupStorageBuildersFromConfig
	storageConfig := &yaml.StorageConfig{
		Type: "sqlite",
		Config: map[string]any{
			"database": ":memory:",
		},
	}

	connectionProvider, err := yaml.SetupStorageBuildersFromConfig(storageConfig)
	if err != nil {
		t.Fatalf("SetupStorageBuildersFromConfig() failed: %v", err)
	}

	conn, err := connectionProvider()
	if err != nil {
		t.Fatalf("connectionProvider() failed: %v", err)
	}
	defer func() {
		if sqlConn, ok := conn.(*yaml.SQLConnection); ok {
			if db := sqlConn.DB(); db != nil {
				if err := db.Close(); err != nil {
					t.Errorf("Failed to close database: %v", err)
				}
			}
		}
	}()

	// Test DB() method
	sqlConn, ok := conn.(*yaml.SQLConnection)
	if !ok {
		t.Fatalf("Expected *SQLConnection, got %T", conn)
	}

	returnedDB := sqlConn.DB()
	if returnedDB == nil {
		t.Error("DB() should not return nil")
	}
}

func TestSQLConnection_Underlying(t *testing.T) {
	// Create SQLConnection through SetupStorageBuildersFromConfig
	storageConfig := &yaml.StorageConfig{
		Type: "sqlite",
		Config: map[string]any{
			"database": ":memory:",
		},
	}

	connectionProvider, err := yaml.SetupStorageBuildersFromConfig(storageConfig)
	if err != nil {
		t.Fatalf("SetupStorageBuildersFromConfig() failed: %v", err)
	}

	conn, err := connectionProvider()
	if err != nil {
		t.Fatalf("connectionProvider() failed: %v", err)
	}
	defer func() {
		if sqlConn, ok := conn.(*yaml.SQLConnection); ok {
			if db := sqlConn.DB(); db != nil {
				if err := db.Close(); err != nil {
					t.Errorf("Failed to close database: %v", err)
				}
			}
		}
	}()

	// Test Underlying() method
	underlying := conn.Underlying()
	if underlying == nil {
		t.Error("Underlying() should not return nil")
	}

	// Verify it returns *sql.DB
	if _, ok := underlying.(*sql.DB); !ok {
		t.Errorf("Underlying() should return *sql.DB, got %T", underlying)
	}

	// Test that DB() and Underlying() return the same value
	sqlConn, ok := conn.(*yaml.SQLConnection)
	if !ok {
		t.Fatalf("Expected *SQLConnection, got %T", conn)
	}

	if sqlConn.DB() != underlying {
		t.Error("DB() and Underlying() should return the same *sql.DB")
	}
}

func TestSetupStorageBuilders(t *testing.T) {
	// Test with nil config (should not error)
	err := yaml.SetupStorageBuilders(nil, nil)
	if err != nil {
		t.Errorf("SetupStorageBuilders(nil) should not error, got %v", err)
	}

	// Test with sqlite type
	storageConfig := &yaml.StorageConfig{
		Type: "sqlite",
		Config: map[string]any{
			"database": ":memory:",
		},
	}

	dbProvider := func() (*sql.DB, error) {
		return sql.Open("sqlite3", ":memory:")
	}

	err = yaml.SetupStorageBuilders(storageConfig, dbProvider)
	if err != nil {
		t.Errorf("SetupStorageBuilders(sqlite) should not error, got %v", err)
	}

	// Test with unknown type
	unknownConfig := &yaml.StorageConfig{
		Type: "unknown_type",
	}

	err = yaml.SetupStorageBuilders(unknownConfig, nil)
	if err == nil {
		t.Error("SetupStorageBuilders(unknown_type) should error")
	}
}

func TestSetupStorageBuildersFromConfig(t *testing.T) {
	// Test with nil config (should error)
	_, err := yaml.SetupStorageBuildersFromConfig(nil)
	if err == nil {
		t.Error("SetupStorageBuildersFromConfig(nil) should error")
	}

	// Test with sqlite type
	storageConfig := &yaml.StorageConfig{
		Type: "sqlite",
		Config: map[string]any{
			"database": ":memory:",
		},
	}

	connectionProvider, err := yaml.SetupStorageBuildersFromConfig(storageConfig)
	if err != nil {
		t.Fatalf("SetupStorageBuildersFromConfig(sqlite) should not error, got %v", err)
	}
	if connectionProvider == nil {
		t.Fatal("SetupStorageBuildersFromConfig(sqlite) should return connectionProvider")
	}

	// Test the connection provider
	conn, err := connectionProvider()
	if err != nil {
		t.Fatalf("connectionProvider() should not error, got %v", err)
	}
	if conn == nil {
		t.Fatal("connectionProvider() should not return nil")
	}

	// Verify it's a SQLConnection
	sqlConn, ok := conn.(*yaml.SQLConnection)
	if !ok {
		t.Fatalf("connectionProvider() should return *SQLConnection, got %T", conn)
	}

	// Test that we can get the DB
	db := sqlConn.DB()
	if db == nil {
		t.Error("SQLConnection.DB() should not return nil")
	}
	if err := db.Close(); err != nil {
		t.Errorf("Failed to close database: %v", err)
	}

	// Test with unknown type (should return nil, nil)
	unknownConfig := &yaml.StorageConfig{
		Type: "unknown_type",
	}

	connectionProvider, err = yaml.SetupStorageBuildersFromConfig(unknownConfig)
	if err != nil {
		t.Errorf("SetupStorageBuildersFromConfig(unknown_type) should not error, got %v", err)
	}
	if connectionProvider != nil {
		t.Error("SetupStorageBuildersFromConfig(unknown_type) should return nil connectionProvider")
	}
}

func TestSetupStorageFromConfig(t *testing.T) {
	// Test with nil config (should error)
	_, err := yaml.SetupStorageFromConfig(nil)
	if err == nil {
		t.Error("SetupStorageFromConfig(nil) should error")
	}

	// Test with sqlite type and full config
	storageConfig := &yaml.StorageConfig{
		Type: "sqlite",
		Config: map[string]any{
			"database": ":memory:",
			"custom_fields": map[string]any{
				"key": "key TEXT",
			},
			"history": map[string]any{
				"table": "custom_history",
				"custom_fields": map[string]any{
					"ip_address": "ip_address TEXT",
				},
			},
		},
	}

	result, err := yaml.SetupStorageFromConfig(storageConfig)
	if err != nil {
		t.Fatalf("SetupStorageFromConfig() should not error, got %v", err)
	}
	// Verify result components
	if result.Storage == nil {
		t.Error("SetupStorageFromConfig() should set Storage")
	}
	if result.Connection == nil {
		t.Error("SetupStorageFromConfig() should set Connection")
	}
	if result.HistoryStore == nil {
		t.Error("SetupStorageFromConfig() should set HistoryStore when history config is present")
	}

	// Test that connection works
	sqlConn, ok := result.Connection.(*yaml.SQLConnection)
	if !ok {
		t.Fatalf("Connection should be *SQLConnection, got %T", result.Connection)
	}

	db := sqlConn.DB()
	if db == nil {
		t.Error("SQLConnection.DB() should not return nil")
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Errorf("Failed to close database: %v", err)
		}
	}()

	// Test with sqlite type without history config
	storageConfigNoHistory := &yaml.StorageConfig{
		Type: "sqlite",
		Config: map[string]any{
			"database": ":memory:",
			"custom_fields": map[string]any{
				"key": "key TEXT",
			},
		},
	}

	result2, err := yaml.SetupStorageFromConfig(storageConfigNoHistory)
	if err != nil {
		t.Fatalf("SetupStorageFromConfig() without history should not error, got %v", err)
	}
	if result2.HistoryStore != nil {
		t.Error("SetupStorageFromConfig() should not set HistoryStore when history config is absent")
	}

	// Test with storage config that has empty database path (should use default)
	storageConfigDefaultDB := &yaml.StorageConfig{
		Type:   "sqlite",
		Config: map[string]any{},
	}

	result3, err := yaml.SetupStorageFromConfig(storageConfigDefaultDB)
	if err != nil {
		t.Fatalf("SetupStorageFromConfig() with default DB should not error, got %v", err)
	}
	if result3.Connection == nil {
		t.Error("SetupStorageFromConfig() should set Connection even with default DB")
	}
	if sqlConn, ok := result3.Connection.(*yaml.SQLConnection); ok {
		if db := sqlConn.DB(); db != nil {
			if err := db.Close(); err != nil {
				t.Errorf("Failed to close database: %v", err)
			}
		}
	}
}
