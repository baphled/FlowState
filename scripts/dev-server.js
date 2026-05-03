#!/usr/bin/env node
// Starts the FlowState backend server for development.
// This script is used by npm run dev:full to start both backend and frontend.

import { spawn } from 'node:child_process'
import http from 'node:http'
import { fileURLToPath } from 'node:url'
import { dirname, resolve } from 'node:path'

const PORT = 8080
const HOST = 'localhost'
const TIMEOUT_MS = 30000

// Resolve flowstate binary path relative to the project root
const __dirname = dirname(fileURLToPath(import.meta.url))
const BACKEND_DIR = resolve(__dirname, '..')
const FLOWSTATE_BIN = resolve(BACKEND_DIR, 'flowstate')

function startBackend() {
  console.log('Starting FlowState backend on', HOST + ':' + PORT + '...')
  
  const backend = spawn(FLOWSTATE_BIN, ['serve', '--port', String(PORT)], {
    cwd: BACKEND_DIR,
    stdio: ['ignore', 'pipe', 'pipe'],
  })
  
  backend.stdout.on('data', (data) => {
    process.stdout.write(data)
  })
  
  backend.stderr.on('data', (data) => {
    process.stderr.write(data)
  })
  
  backend.on('error', (err) => {
    console.error('Failed to start backend:', err.message)
    process.exit(1)
  })
  
  backend.on('exit', (code) => {
    console.log('Backend exited with code:', code)
    process.exit(code ?? 0)
  })
  
  return backend
}

function waitForHealth(deadline) {
  return new Promise((resolve, reject) => {
    const req = http.request({
      hostname: HOST,
      port: PORT,
      path: '/health',
      method: 'GET',
    }, (res) => {
      if (res.statusCode === 200) {
        console.log('Backend is ready!')
        resolve()
      } else {
        reject(new Error(`Health check failed: ${res.statusCode}`))
      }
      res.resume()
    })
    
    req.on('error', (err) => {
      if (Date.now() > deadline) {
        reject(err)
        return
      }
      // Retry
      setTimeout(() => waitForHealth(deadline).then(resolve).catch(reject), 500)
    })
    
    req.end()
  })
}

async function main() {
  const backend = startBackend()
  
  try {
    const deadline = Date.now() + TIMEOUT_MS
    await waitForHealth(deadline)
    console.log('Backend started successfully, keeping process alive...')
    
    // Keep the process alive - don't exit
    process.on('SIGINT', () => {
      console.log('Shutting down backend...')
      backend.kill()
      process.exit(0)
    })
    
    process.on('SIGTERM', () => {
      console.log('Shutting down backend...')
      backend.kill()
      process.exit(0)
    })
    
  } catch (err) {
    console.error('Timeout waiting for backend:', err.message)
    backend.kill()
    process.exit(1)
  }
}

main()