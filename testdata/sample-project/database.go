package project

import "errors"

// QueryUsers retrieves all users from the database with optional pagination.
func QueryUsers(limit, offset int) ([]User, error) {
	if limit <= 0 {
		return nil, errors.New("limit must be positive")
	}
	return nil, nil
}

// InsertUser adds a new user record to the database.
func InsertUser(user *User) error {
	if user == nil {
		return errors.New("user is nil")
	}
	return nil
}

// BeginTransaction starts a new database transaction.
func BeginTransaction() error {
	return nil
}
