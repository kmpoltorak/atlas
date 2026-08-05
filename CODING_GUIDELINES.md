# Coding Guidelines
Language:
Go
Architecture:
Clean Architecture
Patterns:
Repository
Factory
Strategy
Logging:
Zap
Configuration:
Viper
Database:
sqlx
Rules
No panic()
No fmt.Println()
No global variables
No SELECT *
Always use context.Context
Wrap errors
Return typed errors
Small interfaces
Small packages
Small commits
Write benchmarks for critical code.